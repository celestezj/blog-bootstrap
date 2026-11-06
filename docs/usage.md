# blog-bootstrap 使用手册

本手册面向人类操作者。AI 必须遵守的硬规则见 [`agent-install.md`](agent-install.md)，两者是互补关系：这里解释“为什么”和“怎么用”，AI 手册规定“不得做什么”。

## 0. 什么算安装成功

只有下面这条命令返回 `"ok": true` 且退出码为 0，才算完成：

```bash
blog-bootstrap verify --strict --json
```

`run` 的退出码、`status` 的状态文件、日志、单个步骤的探测结果，都**不是**成功证据。

## 1. 它做什么

按顺序准备五步，每步都有独立的探测（Inspect）、执行（Apply）和验证（Verify）：

| 步骤 ID | 必需 | 内容 |
|---|---|---|
| `auth.ssh` | 是 | 探测/安装 git，配置 SSH key、Host 别名，完成公钥登记，验证读权限与部署仓库写权限 |
| `node` | 是 | 探测/安装 Node.js 与 npm，按 `.node-version` 对账 |
| `hexo` | 否 | 安装全局 `hexo-cli`；失败不影响后续，构建始终用项目内 hexo |
| `workspace` | 是 | clone 博客仓库，拉取主题子模块并校验 gitlink |
| `preview` | 是 | `npm ci`、构建，启动临时 server 验证 HTTP 200 后回收进程 |

它**不会** commit、push、deploy，也不会修改全局 git 身份、不会删除已存在的目标目录。

## 2. 获取程序

### 已发布

macOS / Linux：

```bash
curl -fsSLO https://raw.githubusercontent.com/celestezj/blog-bootstrap/main/install.sh
sh install.sh
```

Windows（PowerShell）：

```powershell
Invoke-WebRequest -UseBasicParsing -Uri https://raw.githubusercontent.com/celestezj/blog-bootstrap/main/install.ps1 -OutFile install.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

脚本只负责下载、SHA256 校验、安装并调用二进制，不含安装业务逻辑。它们默认指向 `https://github.com/celestezj/blog-bootstrap`，**仓库或 Release 不存在时会下载失败**。

### 尚未发布

在开发机构建后把二进制拷到目标机器：

```bash
go build -o blog-bootstrap ./cmd/blog-bootstrap
```

```powershell
go build -o blog-bootstrap.exe ./cmd/blog-bootstrap
```

目标机器不需要 Go。可用 `blog-bootstrap version` 确认程序可运行。

## 3. 配置文件

### 查找规则

- 默认读取**当前工作目录**下的 `blog-setup.yml`，不是 exe 所在目录。
- 用 `--config <path>` 指定其他位置。标志要放在子命令后面：

  ```powershell
  .\blog-bootstrap.exe plan --config D:\blog\blog-setup.yml --json
  ```

- 配置中的相对路径（`workspace.dir`、`auth.key`）按**配置文件所在目录**解析，与从哪个目录启动无关。
- 仓库只提供 `blog-setup.example.yml`；`blog-setup.yml` 被 `.gitignore` 忽略，需要自己复制并填写。

### 字段

| 字段 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `version` | 是 | 无 | 当前只支持 `1` |
| `repos.source` | 是 | 无 | 博客源码仓库 |
| `repos.theme` | 是 | 无 | Hexo 主题仓库 |
| `repos.deploy` | 是 | 无 | 部署仓库，必须精确命名为 `<owner>.github.io` |
| `git_identity.name` | 是 | 无 | commit 作者名 |
| `git_identity.email` | 是 | 无 | commit 作者邮箱 |
| `auth.alias` | 否 | owner | SSH Host 别名；远程地址中的 `github.com` 会被替换 |
| `auth.key` | 否 | `~/.ssh/id_ed25519_<owner>` | 专用私钥路径 |
| `toolchain.strategy` | 否 | `system_first` | `system_first` 或 `isolated` |
| `toolchain.node_version` | 否 | `22` | clone 后以仓库 `.node-version` 为准 |
| `workspace.dir` | 否 | 源仓库名 | 工作目录 |
| `workspace.branch` | 否 | `master` | 博客仓库分支 |

硬规则：

- 三个仓库必须属于同一个 GitHub 账号；
- 部署仓库名必须是 `<owner>.github.io`；
- 只支持 SSH 鉴权；
- 目标目录已存在但不是同一个仓库时停止，不覆盖、不删除。

## 4. 四个必须先理解的概念

### 4.1 为什么不是“一键安装”

因为三个阶段对应三种信任边界，不是流程冗余：

| 阶段 | 副作用 | 作用 |
|---|---|---|
| `plan` | 只读 | 列出将安装的包、写入/备份的文件、生成或复用的 key，并计算 consent hash |
| `run --consent <hash>` | 修改机器 | 用户确认后执行；执行前重新计算 plan，hash 不符就拒绝 |
| `verify --strict` | 不改系统；更新状态与收据 | 独立进程重跑全部探针，是唯一成功信号 |

直接运行 `blog-bootstrap.exe` 会返回 `USAGE`。原因：

1. 它要安装软件、写 `~/.ssh/config`、生成私钥，必须先展示具体动作再执行。
2. 工具经常由 AI 操作，协议要求 AI 先展示 plan、取得用户明确同意，再传 hash，避免“替你同意”。
3. `run` 不能给自己发合格证；只有独立探针能判定成功。
4. 分开后 `plan`、`run`、`verify` 都可以单独重跑，续跑和排障才有确定性。

可以把三条命令包进脚本，但不应跳过“查看并确认 plan”这一步。

### 4.2 `--json` 为什么每条命令都要带

`--json` 是**单次调用**的开关，CLI 没有常驻状态，也没有全局配置：

- 不带：人类可读文本；
- 带：稳定信封 `{"schema_version":1,"ok":...,"command":...,"data":...}`，错误也转为 JSON。

同一个命令要同时服务人和 AI，所以默认给人看，机器模式显式开启。位置是 `blog-bootstrap <command> --json`；写成 `blog-bootstrap --json plan` 会被当成未知命令。

### 4.3 `--strict` 是什么意思

**strict = 不依据缓存里的步骤状态下结论，现在现场重新探测。** `verify --strict` 对每一步重新执行 Verify 探针；它可能用状态文件里记录的工具绝对路径或 SSH endpoint 作为起点，但成功与否只由本次探测决定。

目前没有“非 strict 的 verify”模式：

- 不带 `--strict` 也不带 `--step`，命令直接报用法错误；
- 想看缓存结果用 `status --json`，而文档明确说它不作为成功证据；
- `verify --step <id>` 是另一种允许形式，同样现场探测，只是范围限定为单步，用于人机轮询。

保留这个显式开关，是为了让“我真的重跑了全部探针”成为调用者的明确意图。

### 4.4 `run --consent <hash>` 的 hash 是什么

它是 `plan --json` 输出中 `data.hash` 的值：

```json
{
  "schema_version": 1,
  "ok": true,
  "command": "plan",
  "data": {
    "owner": "celestezj",
    "steps": [
      { "id": "auth.ssh", "required": true, "satisfied": false }
    ],
    "hash": "<64 位十六进制>"
  }
}
```

该哈希覆盖：配置身份与解析后的路径、配置内容摘要、探测到的环境事实，以及每一步的 Inspect 结果（是否已满足、actions、evidence、warning）。

`run` 会重新 Inspect、重建 plan、重算 hash，再与传入值比较：

- 一致：执行；
- 不一致：返回 `CONSENT_MISMATCH`（退出码 2），要求重新展示计划并取得同意。

它把“用户同意”绑定到当前机器上将要发生的**具体动作**，不是密码或身份认证。配置或环境变化后旧 hash 必然失效，这是预期行为。

PowerShell 取 hash 的正确写法：

```powershell
$plan = .\blog-bootstrap.exe plan --json | ConvertFrom-Json
$plan.data.steps | Format-Table id, required, satisfied, warning
.\blog-bootstrap.exe run --consent $plan.data.hash --json
.\blog-bootstrap.exe verify --strict --json
```

## 5. 标准流程

```bash
# 1. 生成并审阅计划
blog-bootstrap plan --json

# 2. 用户确认后执行（hash 来自上一步 data.hash）
blog-bootstrap run --consent <hash> --json

# 3. 最终验证
blog-bootstrap verify --strict --json
```

`run` 遇到需要人操作的情况时，返回：

```json
{
  "schema_version": 1,
  "ok": false,
  "next_action": {
    "type": "human",
    "code": "AUTH_KEY_NOT_ADDED",
    "open_url": "https://github.com/settings/ssh/new?...",
    "message": "请在浏览器中确认已登录目标账号，点击 Add SSH key。完成后回来，我会自动检测。",
    "expected": "Hi <account>!",
    "poll": {
      "command": "blog-bootstrap verify --step auth.ssh --json",
      "interval_seconds": 5,
      "timeout_seconds": 180
    }
  }
}
```

处理方式：

1. 把 `message` 原样转述给用户；
2. 让用户打开 `open_url`，工具不代替点击；
3. 用户完成后重跑 `poll.command`，或直接 `verify --step auth.ssh --json`；
4. 最终仍以 `verify --strict --json` 为准。

安装后可选执行 `blog-bootstrap doctor --json` 做健康检查；它只给警告，不声称已验证。

## 6. 中断后如何续跑

支持续跑。没有独立的 `resume` 命令，重新 `plan` → `run --consent` 即可。

机制：

1. **每一步执行前重新 Inspect。** 已装好的 git 会被识别并跳过；半完成步骤只补剩余动作；已 clone 的仓库会校验 remote、分支、子模块。
2. **状态文件记录辅助信息。** `<workspace>/.bootstrap/state.json` 保存工具绝对路径、便携工具位置、SSH endpoint、别名、步骤证据和收据。即使删掉它也能靠现场探测续跑，但会丢失这些缓存。
3. **崩溃后的锁恢复。** 进程被杀留下的 `run.lock`，下次启动检测到 PID 不存在会自动接管。

注意：安装 git 后环境已变化，旧 consent hash 会失效。第二次必须重新 `plan` 拿新 hash：

```bash
blog-bootstrap plan --json            # 新 hash
blog-bootstrap run --consent <新hash> --json
blog-bootstrap verify --strict --json
```

其他边界：

- 中断在 clone 中途且目录 remote 不匹配：返回 `REPO_CONFLICT` 并停止，不删除目录；
- 中断在构建中途：重新跑 `npm ci`/构建，缓存键匹配时跳过依赖安装；
- 查看停在哪：`blog-bootstrap status --json` 或 `blog-bootstrap next --json`。

## 7. 命令参考

| 命令 | 副作用 | 说明 |
|---|---|---|
| `plan --config <path> --json` | 无 | 解析配置、校验、输出计划与 consent hash |
| `run --consent <hash> --json` | 有 | 执行已确认的计划 |
| `verify --strict --json` | 更新状态与收据 | 现场重跑全部探针，唯一成功信号 |
| `verify --step <id> --json` | 更新该步状态 | 只重跑一个步骤的探针 |
| `status --json` | 无 | 查看缓存状态，不作为成功证据 |
| `next --json` | 无 | 返回下一步与建议命令 |
| `doctor --json` | 无 | 安装后健康检查，只给警告 |
| `agent-guide` | 无 | 输出 AI 安装手册 |
| `version` | 无 | 输出版本号 |

## 8. 退出码

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 2 | 需要人工处理 |
| 3 | 可重试 |
| 4 | 致命错误 |
| 5 | 验证失败 |

`CONFIG_OWNER_MISMATCH`、`AUTH_WRONG_ACCOUNT`、`REPO_CONFLICT` 等确定性失败不会自动重试；错误码与处理动作的完整列表见 [`agent-install.md`](agent-install.md) 第 5 节。

## 9. 文件与日志

相对于解析后的工作目录（`workspace.dir`）：

| 路径 | 内容 |
|---|---|
| `.bootstrap/state.json` | 步骤状态、工具路径、endpoint、别名、收据 |
| `.bootstrap/run.lock` | 防并发锁，运行结束后删除 |
| `.bootstrap/logs/` | 每一步的日志 |
| `.bootstrap/tools/` | 便携版 git/Node（如使用） |
| `.bootstrap/changes.json` | 安装动作记录，供人工回滚 |

## 10. 常见问题

**Q：为什么直接运行 `blog-bootstrap.exe` 不能安装？**
见 4.1。三段式是安全边界，不是多余步骤。

**Q：还没发布到 GitHub，install 脚本怎么知道从哪下载？**
它不知道。默认地址写死为 `https://github.com/celestezj/blog-bootstrap`，没有 Release 就会 404。未发布时直接 `go build` 并拷贝二进制，或用 `BLOG_BOOTSTRAP_BASE_URL` + `BLOG_BOOTSTRAP_VERSION` 指向镜像。

**Q：plan 会自动读取同目录的 `blog-setup.yml` 吗？**
会，但“同目录”指当前工作目录，不是 exe 所在目录；用 `--config` 可覆盖。

**Q：`--json` 能不能只写一次？**
不能。每次命令都是独立进程，需要各自携带。

**Q：`verify` 为什么不带 `--strict` 会报错？**
因为没有“读缓存就算验证”的模式。缓存看 `status`，验证必须现场探测。

**Q：hash 去哪里拿？**
`plan --json` 的 `data.hash`。

**Q：安装到一半退出，下次会从头再来吗？**
不会。重新 `plan` 后 `run` 会跳过已满足的步骤，继续剩余动作；详情见第 6 节。

**Q：会修改我的全局 git 身份吗？**
不会写入任何 git 配置。`git_identity` 只通过 `git -c` 临时传给写权限探针，现有全局或仓库本地身份不受影响。

**Q：会帮我 commit、push 或 deploy 吗？**
不会。安装流程不包含发布；写权限只用 `git push --dry-run` 探测。

## 11. 安全边界

- 仅支持 SSH 鉴权；浏览器只打开 `https://github.com` 下的地址。
- 私钥内容不进入日志、JSON 或浏览器；对外只输出公钥指纹。
- 所有外部命令通过 argv 调用，不经过 shell。
- 下载的便携工具和发布归档都校验 SHA256。
- plan 是唯一的改动授权；`--force` 不存在。
