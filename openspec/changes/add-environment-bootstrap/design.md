# Design

## Context

这是一个全新的独立仓库，安装器必须在 git 和 Node.js 都不存在的机器上运行；博客源码仓库保持私有，因此安装器本身通过公开仓库和 GitHub Releases 分发。鉴权只支持 SSH，三个仓库必须属于同一账号。构建机器当前没有 Go，但 Go 只用于开发与发布，运行产物是自包含二进制。动机见 `proposal.md`。

## Goals / Non-Goals

**Goals:**

- 单个无运行时依赖的二进制，覆盖 Windows/macOS/Linux 的 amd64/arm64。
- 每一步都能独立探测、验证、重试和恢复，结果不可伪造。
- 用户和 AI 使用同一套行为，只是输出模式不同。
- 配置面最小，默认值由仓库地址和约定推导。
- 私钥、令牌和其他秘密不进入日志、JSON 或浏览器。

**Non-Goals:**

- 不执行 deploy、push、commit，不创建 CI secret，不修改 Pages 设置。
- 不支持 Docker。
- 不支持 HTTPS/PAT 鉴权，不支持一次运行切换多个 GitHub 账号。
- 不提供 GUI，不做自动卸载系统包。
- 不实现静默自更新。

## Decisions

### D1: Go 单文件

使用 Go 1.24+，依赖限定为标准库 + `gopkg.in/yaml.v3` + `golang.org/x/term` + `golang.org/x/sys`。GoReleaser 交叉编译六个平台目标。

备选方案是 Bun 编译 TS 和 sh/ps1 引导 Node。选择 Go 的原因是冷启动最可靠、体积小、无需先安装任何运行时；代价是安装器逻辑与博客项目不同语言，通过把逻辑限制在进程、文件、网络和 TTY 操作来控制维护成本。

### D2: 配置与校验

必填只有三个仓库 URL 和 commit 身份。URL 只接受 `git@github.com:owner/repo.git` 或 `https://github.com/owner/repo.git`，带自定义 host 的地址直接拒绝，否则无法可靠解析 owner。

预检阶段强制三条规则：三个 owner 一致、deploy 精确等于 `<owner>.github.io`、目标目录不冲突。可选字段缺失时使用确定默认值：alias 取 owner，key 取 `~/.ssh/id_ed25519_<owner>`，目录取源仓库名，分支取 `master`，预装 Node 取 22，工具链策略取 `system_first`。

真实配置 `blog-setup.yml` 不入库，仓库只提交 `blog-setup.example.yml`，避免他人误用原作者的仓库地址。

### D3: SSH 鉴权

一台机器只为一个目标账号准备一套凭据。先探测现有 key，能通过 greeting 证明属于目标账号就复用；否则生成 ed25519 专用 key。Host 别名使用带标记的配置块，写入前备份，并设置 `IdentitiesOnly yes`；已存在的非标记块不覆盖，别名冲突时改用 `<alias>-blog`。

别名是 URL 路由机制：所有远程地址中的 `github.com` 被替换为别名，这样同一台机器上的其他账号不会抢认证。公钥登记使用 GitHub 预填页面，用户点击后安装器轮询 `ssh -T`。GitHub 在无 shell 权限时返回退出码 1，因此成功判断只能解析 `Hi <owner>!`，不能看退出码。

账号错误是确定性失败：同一把公钥不能同时绑定两个账号，因此返回 `AUTH_WRONG_ACCOUNT`，提供“删除错误账号上的 key”或“重新生成 key”两条路径，不自动重试。greeting 之后再执行源仓库 `ls-remote` 和部署仓库 `push --dry-run`，分别证明读和写权限。

网络端点先试 `github.com:22`，失败后回退 `ssh.github.com:443`，结果只写入状态文件，不暴露给配置。默认生成无口令 key 以支持非交互流程，私钥权限 0600；需要口令的用户可显式开启并配合 ssh-agent。

备选方案是 repo-local `core.sshCommand` 完全不碰 `~/.ssh/config`，但网络回退时需要改写所有 URL，且子模块与部署脚本都要单独处理；别名方案更简单，也符合现有仓库的用法。

### D4: 状态机与授权

每个步骤实现 detect、apply、verify 三个行为。detect 只判断“是否已满足”，apply 只执行动作，verify 用独立探针产生成功证据；apply 永远不能把步骤标记为完成。

执行前输出 plan，列出要安装的包、要写入或备份的文件、要生成的 key 与别名，并计算 SHA256；`run` 必须携带同一个哈希。配置或环境变化导致 hash 不同时拒绝执行。这一机制把 AI 的同意绑定到具体动作，而不是一句“用户同意了”。

状态文件是缓存，不是事实。`verify --strict` 每次重新探测；步骤验证失败时，其下游产物标记为失效并重新验证。网络失败按退避重试，确定性失败立即停止。运行期间持有带 PID 的锁，避免 AI 和用户同时执行。

所有命令提供带 `schema_version` 的 JSON 和固定退出码：0 成功、2 需人工、3 可重试、4 致命、5 验证失败。`human_required` 带上提示、URL、期望结果和轮询参数，AI 只需转述和等待，不需要理解细节。

### D5: 工具链

优先使用系统包管理器安装 git 与 Node.js；不可用、无权限或失败时回退到 `.bootstrap/tools/` 下的便携版本，下载后校验 SHA256。已有且版本满足的系统工具一律复用，不覆盖系统 Node。

Node 版本以博客仓库的 `.node-version` 为准。安装器在 clone 前只能使用配置或默认值预装，clone 后读取 `.node-version` 做对账；不一致时使用项目内便携 Node，而不是替换系统 Node。依赖安装使用 `npm ci`，以 lockfile 哈希和 Node 版本作为缓存键。

全局 `hexo-cli` 只是可选便利项，失败不阻断；构建和验收始终使用项目内 hexo。所有子进程使用状态文件中记录的绝对路径，并显式构造 PATH，避免 Windows 安装后当前终端 PATH 未刷新的问题。

Windows 上 `npm`、`hexo` 是 `.cmd` shim，Go 的 `CreateProcess` 不能直接执行批处理；实现时调用 `node.exe` + `npm-cli.js` 或 hexo 的 JS 入口，不拼 `cmd /c` 字符串。需要提权或交互但当前为非 TTY 时，返回 `human_required`，不挂起等待。

### D6: 工作区与构建验证

目标目录已存在且 remote 不匹配时立即停止，不删除。clone 使用别名改写后的源仓库地址，完成后验证 remote 和分支。

主题优先按子模块处理：在本地覆盖 `submodule.themes/A4.url` 后递归初始化，不修改 `.gitmodules`；验证 `git submodule status` 无前导 `-`，且主题 HEAD 等于父仓库记录的 gitlink。若仓库没有声明该子模块，则按配置地址克隆并 checkout 固定提交，避免默认分支漂移。

构建依次执行 `hexo clean`、`hexo generate`、`node clean-search.js`，要求 `public/index.html` 和 `public/search.json` 存在且非空。预览使用空闲端口启动临时 `hexo server`，轮询 HTTP 200 后回收整个进程树；不执行永不退出的 `npm run preview`。失败时保存输出尾部并返回日志路径。

安装过程写入 `changes.json`，记录系统包、便携工具、SSH 配置块和备份路径，供用户手动回滚；不自动卸载系统包。

### D7: 分发与发布

公开仓库承载源码、文档和示例配置，GitHub Releases 承载二进制与 `SHA256SUMS`。入门脚本只负责选择平台、下载、校验和执行，不包含业务逻辑。版本通过 ldflags 注入，`blog-bootstrap version` 输出机器可读版本。

二进制未签名会触发 SmartScreen 或杀软告警；v1 用校验和与文档缓解，代码签名留到后续。自动更新不在范围内。

### D8: AI 协议

`agent-guide` 输出与 `docs/agent-install.md` 相同的内容，CI 校验一致性。AI 的成功判据只有 `verify --strict --json` 的 `ok` 与退出码 0；状态文件、日志和单步退出码都不算证据。

AI 必须先把 plan 展示给用户，取得同意后才传 consent hash。遇到 `human` 类型时原样转述，不模拟点击、不删除目录、不修改全局 git 身份、不执行 commit/push/deploy。输出中只允许出现公钥指纹。

### D9: 安全边界

所有外部命令通过 argv 调用，不经过 shell；URL 使用白名单解析和仓库名正则校验，阻止参数注入。浏览器只打开 `https://github.com` 下的 URL，公钥参数做 URL 编码。

下载的便携工具校验 SHA256。首次连接 GitHub 时校验主机指纹；如果 `ssh.github.com` 与 `github.com` 的主机密钥不同，则随二进制附带已知指纹。不收集遥测，只访问 GitHub、nodejs.org 和系统包管理器。

## Risks / Trade-offs

- **未签名二进制触发 SmartScreen/杀软** → 提供 SHA256、文档说明和手动放行步骤；后续增加代码签名。
- **仅 SSH 无法查询仓库可见性、CI secret、Pages 设置** → `doctor` 只给警告和检查清单，不声称已验证。
- **system-first 会修改机器** → plan 明确列出改动，写入前备份，`changes.json` 支持人工回滚；不自动卸载。
- **默认无口令 key 降低单机防护** → 专用 key、权限 0600、文档建议敏感用户改口令并配合 agent。
- **`ssh -T` 成功退出码为 1** → 只解析 greeting，不依赖退出码。
- **同一公钥不能绑定两个账号** → 错误账号触发确定性失败，提供删除或重建两条路径。
- **Windows 批处理 shim 无法直接执行** → 直接调用 `node.exe` 和 JS 入口。
- **公开仓库下 `ls-remote` 不能证明身份** → greeting 和部署仓库 dry-run 作为权威探针。
- **预填页面在登录跳转后可能丢失参数** → 提供复制公钥并手动添加的兜底说明。

## Migration Plan

新仓库，无历史迁移。完成实现后打 `v0.1.0`，由 blog-delta 的 README 声明最低兼容版本。blog-delta 的兼容性改造（相对子模块 URL、deploy owner 推导、`.node-version`、CI 版本源）是独立变更，不在本仓库执行。安装器在 `.node-version` 缺失时使用默认 22，保证改造前后都能运行。

回滚策略：便携工具可直接删除；SSH 配置块按备份恢复；系统包由用户按 `changes.json` 手动卸载。

## Open Questions

- **`ssh.github.com:443` 的主机密钥（2026-11-03 实测）：** 与 `github.com:22` 当前的 RSA/ECDSA/ED25519 三种主机密钥完全一致（`ssh-keyscan` + `ssh-keygen -lf` 对比）。因此不需要随二进制附带额外指纹；实现保留 `StrictHostKeyChecking=accept-new`，并在端点回退时使用同一校验策略。后续若 GitHub 轮换密钥，以 `ssh-keygen -F` 的首次信任结果为准。
- **`git push --dry-run` 的写权限信号（2026-11-03 实测）：** 对公开部署仓库执行 `ls-remote` 即使使用错误账号也会成功，证明读探针不能代表身份或写权限。用非 owner 账号对 `celestezj/celestezj.github.io` 执行 `push --dry-run` 返回 `ERROR: Permission ... denied`，说明 dry-run 能反映写权限。空仓库与受保护分支尚未实测；实现保留 dry-run 为权威探针，失败即返回确定性的 `AUTH_PERMISSION_INSUFFICIENT`，不降级为警告。后续任务：在首次真实安装（7.1）时记录空/受保护仓库的结果。
- **`settings/ssh/new` 查询参数是否在登录跳转后保留：** 尚未做交互式实测，无法在无浏览器登录的环境中验证。实现已同时提供预填 URL、公钥内容与指纹，并在 `AUTH_KEY_NOT_ADDED` 的提示中说明可手动复制公钥。后续任务：在首次真实登记（7.1）时确认；若参数丢失，保持手动粘贴兜底，不改协议。
