# 架构与实现原理

本文说明 `blog-bootstrap` 的组件划分、数据流和设计约束，面向维护者和需要排障的高级用户。产品级决策的完整论证见 [`../openspec/changes/add-environment-bootstrap/design.md`](../openspec/changes/add-environment-bootstrap/design.md)。

## 1. 设计目标

- **自包含**：单个 Go 二进制，无运行时依赖，覆盖 Windows/macOS/Linux 的 amd64/arm64。
- **可恢复**：任何一步中断后，重新运行都能从现场状态继续，而不是从头再来。
- **不可伪造**：状态文件是缓存，只有独立探针能证明成功。
- **同一套行为服务人和 AI**：命令相同，只是输出模式和硬规则不同。
- **最小配置面**：必填只有三个仓库和 commit 身份，其余由约定推导。
- **秘密不出本机**：私钥不进入日志、JSON 或浏览器。

非目标：不执行 deploy/push/commit，不支持 Docker，不支持 HTTPS/PAT 鉴权，不提供 GUI，不自动卸载系统包，不自更新。

## 2. 包结构

| 包 | 职责 |
|---|---|
| `cmd/blog-bootstrap` | 进程入口，注入版本号，调用 `app.Main` |
| `internal/app` | 命令分发、flag 解析、输出渲染、退出码映射 |
| `internal/config` | 读取/校验 `blog-setup.yml`，应用默认值，解析 owner 与路径 |
| `internal/core` | `Context`（配置、状态、执行器、HTTP、浏览器、日志、时钟）与日志 |
| `internal/plan` | 汇总各步 Inspect 结果，构建 plan 并计算 consent hash |
| `internal/run` | 编排引擎：Inspect → Apply → Verify、重试、下游失效、写收据 |
| `internal/steps` | 五个步骤的实现：`auth.ssh`、`node`、`hexo`、`workspace`、`preview` |
| `internal/sshauth` | key 发现/生成、Host 块、greeting、轮询、endpoint 回退、读写探针 |
| `internal/toolchain` | git/Node/hexo 探测、包管理器或便携安装、版本对账 |
| `internal/workspace` | clone、子模块/主题、`npm ci`、构建、临时 server、进程树回收 |
| `internal/state` | 状态缓存、步骤状态、路径、endpoint、收据，原子写入 |
| `internal/lock` | 带 PID 的跨进程锁，stale 接管 |
| `internal/protocol` | JSON 信封、错误分类、退出码、`next_action` |
| `internal/execx` | 仅通过 argv 执行外部命令；测试用 fake 执行器 |
| `internal/netx` | 网络失败分类（超时、拒绝、不可达） |
| `internal/browser` | 仅打开 `https://github.com` 下的 URL |
| `internal/platform` | Windows 控制台 UTF-8 等平台适配 |
| `internal/atomicfile` | 临时文件 + rename 的原子写 |
| `internal/agent` | 内嵌 AI 安装手册，保证 `agent-guide` 与文档一致 |
| `internal/repo` | GitHub 仓库 URL 解析与规范化 |

## 3. 命令数据流

### prepare

所有需要环境的命令都先走 `prepare`：

```
读取配置文件 → config.Resolve（校验 + 默认值 + 绝对路径）
           → state.Open(<workspace>/.bootstrap/state.json)
           → 构造 core.Context
           → steps.NewAll(ctx) 生成步骤列表
```

配置中的相对路径在这里锚定到**配置文件所在目录**，状态和日志锚定到解析后的 `workspace.dir`。

### plan

```
prepare → 对每个 step 执行 Inspect（只读）
        → 汇总为 StepPlan（id/required/satisfied/actions/evidence/warning）
        → plan.Build：加上配置身份、路径、环境事实，做规范 JSON，算 SHA256
        → 输出 data.hash
```

### run

```
prepare → 获取 .bootstrap/run.lock
        → 重新读取 state（等待锁期间状态可能已变化）
        → 再次 Inspect 并重建 plan
        → 与 --consent 比较，不一致则 CONSENT_MISMATCH
        → 记录 plan hash
        → engine.Run
        → 释放锁
```

`run` 必须携带 consent，因为它会修改机器。

### verify

```
prepare → 获取同一把 run.lock
        → 重新读取 state
        → --step：只对指定步骤执行 Verify
        → --strict：对全部步骤执行 VerifyStrict
        → 全部必需步骤通过后写收据
```

`verify` 与 `run` 共用锁，因此二者不会同时修改状态文件。

### status / next / doctor

- `status` 只读缓存，不探测；
- `next` 读取缓存并给出下一步命令；优先报告失败的必需步骤，其次未完成的必需步骤，最后可选步骤；
- `doctor` 做安装后健康检查，只输出警告。

## 4. 执行模型

每个步骤实现三个方法：

```go
type Step interface {
    ID() string
    Required() bool
    Inspect(ctx context.Context) (Inspection, error)
    Apply(ctx context.Context) error
    Verify(ctx context.Context) (map[string]string, error)
}
```

### Run

对每一步：

1. `Inspect`：判断是否已满足，并给出将要执行的动作。
2. 已满足 → 跳过 `Apply`；否则执行 `Apply`，失败按错误类别重试。
3. `Verify`：成功则把步骤标记为 `verified` 并写入状态。
4. 可选步骤在 Inspect/Apply/Verify 任一阶段失败时标记 `skipped`，记录 warning，继续执行。
5. 必需步骤失败时，将当前步骤标记 `failed`，后续步骤标记 `invalid`，立即停止。
6. 全部结束后写收据。

关键约束：**Apply 永远不能把步骤标记为成功**。它只负责产生副作用，成功与否只由 Verify 决定。

### 运行时可观测性

`core.Context.Progress` 是唯一的进度输出通道，`Sayf` 写入一行人类可读信息：

- 文本模式输出到 stdout；
- JSON 模式输出到 stderr，保证 stdout 只有一个 JSON 信封；
- `Progress == nil` 时静默，单元测试和库调用不受影响。

Engine 输出步骤序号、动作列表、执行/验证状态；步骤实现输出具体耗时操作（安装、下载、clone、`npm ci`、构建、SSH 轮询）。便携工具下载通过 `Portable.Progress` 每 2 秒报告字节数。

`auth.ssh` 的登记流程也走同一通道：终端打印账号、公钥、指纹和 URL；交互式终端轮询并显示次数与耗时，非交互式立即返回 `human` 错误，避免 AI 调用被阻塞 180 秒。

### VerifyStrict

不执行任何 Apply，对每一步重新运行 Verify：

- 必需步骤失败 → 返回验证失败（退出码 5）；
- 可选步骤失败 → 标记 `skipped`，记录 warning，不阻断；
- 全部通过 → 写收据并返回 `ok: true`。

收据记录 plan hash、owner、别名、endpoint、工作目录、工具路径、每步证据和公钥指纹。收据是“这一次现场探测通过”的证明，不是永久免检凭证。

## 5. consent hash

`plan.hash` 是 SHA256，输入覆盖所有可能改变动作的字段：

- schema 版本、owner、alias、key 路径；
- 三个仓库 URL、工作目录、分支、Node 版本、工具链策略；
- 配置文件内容摘要；
- 探测到的环境事实；
- 每一步的 `satisfied`、`actions`、`evidence`、`warning`。

因此只要配置、工具版本或机器状态变化，hash 就会变。`run` 重新计算并比较，避免“用户批准的是 A，执行的是 B”。

## 6. 状态、锁与收据

### state.json

位于 `<workspace>/.bootstrap/state.json`，由 `state.Store` 管理：

- `run_id`、`plan_hash`；
- 每步状态：`pending` / `applied` / `verified` / `failed` / `invalid` / `skipped`；
- 探测到的工具绝对路径、便携工具位置、SSH endpoint、最终别名；
- `npm ci` 缓存键；
- 收据。

写入使用 `internal/atomicfile`（临时文件 + rename），避免半写文件。

### 为什么状态不是事实

`run` 和 `verify` 每次都重新 Inspect/Verify，不依据状态决定“是否已满足”。状态文件只用于：

- 记住工具路径和 endpoint，减少重复探测；
- 记录证据和失败原因；
- 失败时把下游标记为 `invalid`；
- 保存收据。

即使状态文件被删除，重新运行也能靠现场探测继续。

### 锁

`run` 和 `verify` 都持有 `.bootstrap/run.lock`：

- 锁文件包含 PID、主机名、启动时间、run_id；
- 同一台机器上 PID 仍存活 → 返回 `LOCK_HELD`（可重试）；
- PID 已不存在或来自其他主机 → 视为 stale，通过 rename 认领后接管；
- 并发接管有竞态保护，最终只有一个进程成功；
- 新鲜但不可读的锁文件有 2 秒宽限期，避免误抢正在写入的锁。

## 7. 五个步骤的内部实现

| 步骤 | 关键实现 |
|---|---|
| `auth.ssh` | 探测 git/ssh；生成或复用 ed25519 key；写入带标记的 `~/.ssh/config` 块并备份；用 `ssh -T` 解析 `Hi <owner>!`；22 → `ssh.github.com:443` 回退；`git ls-remote` 读探针；`git push --dry-run` 写探针 |
| `node` | 探测系统 Node/npm；缺失时按平台用 winget/brew/apt/dnf/pacman，失败回退便携 Node；clone 后按 `.node-version` 对账，不覆盖系统 Node |
| `hexo` | 可选步骤；只要求全局 `hexo-cli` 可执行并返回版本号；构建不使用它 |
| `workspace` | 检查目录冲突；clone 并校验 remote/分支；本地覆盖 `submodule.<name>.url` 后初始化子模块；校验无前导 `-` 且 HEAD 等于 gitlink；无子模块声明时独立克隆主题并固定提交 |
| `preview` | `npm ci`（缓存键 = lockfile + Node 版本）；`hexo clean/generate`；校验 `public/index.html` 与 `public/search.json`；选空闲端口启动临时 server，轮询 HTTP 200 后回收整个进程树 |

## 8. 失败模型

所有错误都归一化为 `protocol.Error`：

| 类别 | 行为 |
|---|---|
| `human` | 返回 `next_action.type = "human"`，附提示、URL、期望结果、轮询参数 |
| `retry` | 有界重试 + 指数退避（封顶 10 秒），耗尽后转人工 |
| `fatal` | 立即停止 |
| `verify` | 验证失败，退出码 5 |

确定性问题不重试，包括：`CONFIG_OWNER_MISMATCH`、`AUTH_WRONG_ACCOUNT`、`REPO_CONFLICT`、`AUTH_PERMISSION_INSUFFICIENT`。

## 9. 平台适配

- **Windows `.cmd` shim**：不执行 `npm.cmd`/`hexo.cmd`，而是调用 `node.exe` + `npm-cli.js` 或 hexo 的 JS 入口，避免 `CreateProcess` 无法直接执行批处理。
- **进程树回收**：Windows 使用 Job Object，辅以 `taskkill /T`；POSIX 使用进程组和 `SIGKILL`。
- **控制台**：启动时设置 UTF-8，避免中文输出乱码。
- **非 TTY**：需要提权或交互时（包括等待 SSH 公钥登记）返回 `human_required`，不挂起等待。
- **PATH**：子进程使用状态中记录的绝对路径并显式构造 PATH，规避安装后当前终端 PATH 未刷新的问题。

## 10. 安全模型

- 外部命令全部通过 argv 调用，不经过 shell，URL 经过白名单解析和仓库名正则校验。
- 仅支持 SSH；浏览器只打开 `https://github.com` 下的地址，公钥参数做 URL 编码。
- 私钥内容不写入日志、JSON 或浏览器；对外只输出公钥指纹。
- Inspect 阶段的 SSH 探针使用临时 known_hosts，不写入真实 `~/.ssh/known_hosts`。
- 下载的便携工具和发布归档校验 SHA256。
- AI 规则与安全边界见 [`agent-install.md`](agent-install.md)。

## 11. 测试策略与扩展

- 单元测试使用 `execx.Fake` 注入命令输出，覆盖成功、失败、重试三条分支；
- 协议层有 golden JSON 测试，固定 0/2/3/4/5 五种退出码和 `next_action` 形状；
- 工作区测试使用真实临时 git 仓库和 fixture，不 mock git；
- CI 在 Ubuntu/macOS/Windows 上跑 `go vet` 与 `go test`，Linux 额外跑 `-race`；
- `scripts/check-agent-guide.sh` 校验 `agent-guide` 与 `docs/agent-install.md` 一致。

新增步骤时：

1. 在 `internal/steps` 实现 `ID/Required/Inspect/Apply/Verify`；
2. 在 `steps.NewAll` 中按依赖顺序加入；
3. 确保 Inspect 只读、Verify 用独立探针、可选步骤失败可降级；
4. 为 Inspect/Apply/Verify 的失败分支补测试；
5. 如果引入新的确定性错误码，同步更新 `protocol`、`agent-install.md` 和本手册。
