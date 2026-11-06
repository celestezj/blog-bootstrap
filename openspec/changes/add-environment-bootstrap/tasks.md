# Tasks

## 1. 项目骨架与配置

- [x] 1.1 完成 Go module、CLI 入口与 `version` 命令。验证：安装 Go 后 `go build ./cmd/blog-bootstrap` 成功，且 `blog-bootstrap version` 输出版本号。
- [x] 1.2 实现配置解析、可选字段默认值与 schema 校验。验证：表驱动测试覆盖 alias/key/目录/分支缺省值。
- [x] 1.3 实现仓库 URL 规范化、同 owner 校验与 `<owner>.github.io` 命名校验。验证：单元测试覆盖合法地址、别名地址、owner 不一致和非法部署仓库名。
- [x] 1.4 校准 `blog-setup.example.yml` 与 README 的字段说明。验证：示例配置能被配置解析器完整读取，文档中的字段与代码一致。

## 2. 计划、状态机与 AI 协议

- [x] 2.1 实现 plan 生成与 consent hash。验证：测试证明配置或环境变化会使旧 hash 失效。
- [x] 2.2 实现步骤状态机、PID 锁、原子写入的状态文件与 receipt。验证：并发运行被拒绝，中断后可恢复，receipt 只在全部 strict 探针通过时写入。
- [x] 2.3 实现失败分类、退避重试、确定性失败停止与下游失效。验证：测试覆盖网络重试上限、错误账号不重试、前序失败使下游失效。
- [x] 2.4 实现 JSON 输出、固定退出码与 `human_required` 结构。验证：golden tests 覆盖 0/2/3/4/5 五种退出码和 next_action 形状。
- [x] 2.5 实现 `agent-guide` 输出并加入文档一致性检查。验证：本地脚本比较 `agent-guide` 与 `docs/agent-install.md`，不一致时退出非零。

## 3. 工具链

- [x] 3.1 实现 git 与 Node.js 的探测和版本区间判断。验证：注入 fake 命令的测试覆盖“已满足”“过低”“缺失”三种情况。
- [x] 3.2 实现 Windows/macOS/Linux 的 system-first 安装适配（winget/brew/apt/dnf/pacman）。验证：fake 执行器测试覆盖成功、无权限、包管理器缺失三条分支。
- [x] 3.3 实现便携 git/Node 下载、SHA256 校验、解压与绝对路径记录。验证：测试覆盖校验失败删除文件、校验成功写入状态。
- [x] 3.4 实现 `.node-version` 对账与 `npm ci` 缓存键。验证：测试覆盖版本一致跳过、版本不一致改用便携 Node、lockfile 哈希变化触发重装。
- [x] 3.5 处理 Windows `.cmd` shim、UTF-8 控制台和非 TTY 提权。验证：Windows 冒烟测试成功调用 npm/hexo；非 TTY 下需要提权时返回 `human_required`。
- [x] 3.6 实现便携 Node 的用户级 PATH 持久化：`plan` 展示 `update_user_path` 动作，`run` 成功后执行 finalize 写入 `HKCU\Environment\Path` 并记录 `changes.json`，`verify` 不修改 PATH。验证：`internal/userpath` 单元测试覆盖前置、去重、旧版本清理与幂等；`NodeStep` 测试覆盖便携/系统 Node、Windows/非 Windows；`cmdRun` 测试覆盖 finalize 调用、失败降级为 warning、verify 不调用 finalize。

## 4. SSH 鉴权

- [x] 4.1 实现现有 key 探测、ed25519 生成与权限收紧。验证：测试覆盖复用现有 key、无匹配时生成、私钥权限为 0600。
- [x] 4.2 实现 Host 别名标记块、备份与冲突处理。验证：测试证明已有非标记块不被覆盖，冲突时使用备用别名。
- [x] 4.3 实现 URL 别名改写与 greeting 解析。验证：单元测试覆盖 `github.com` 替换、`Hi <owner>!` 成功、GitHub 退出码 1、跨平台换行。
- [x] 4.4 实现浏览器预填页面、轮询与错误账号处理。验证：fake greeting 测试覆盖成功、超时、`Hi <其他账号>!` 三条路径。
- [x] 4.5 实现源仓库读探针、部署仓库 `push --dry-run` 写探针与 `github.com:22 → ssh.github.com:443` 回退。验证：集成测试记录两条 Open Question 的实测结果，并在 design.md 中更新结论。

## 5. 工作区与构建

- [x] 5.1 实现目标目录冲突检测、clone、remote 与分支校验。验证：fixture 仓库测试覆盖同仓库跳过、异仓库停止、remote 不匹配失败。
- [x] 5.2 实现子模块 URL 本地覆盖、递归初始化与 gitlink 校验。验证：fixture 仓库测试覆盖成功、未初始化重试失败、HEAD 不一致。
- [x] 5.3 实现无子模块声明时的独立主题克隆与固定提交校验。验证：fixture 测试覆盖 checkout 正确提交和提交不一致失败。
- [x] 5.4 实现 `npm ci`、hexo 构建与产物校验。验证：fixture 构建后 `public/index.html` 与 `public/search.json` 存在且非空。
- [x] 5.5 实现临时 server 空闲端口选择、HTTP 200 轮询与进程树回收。验证：测试覆盖端口占用切换、超时失败、Windows/POSIX 进程清理。
- [x] 5.6 实现日志尾部捕获与 `changes.json` 安装记录。验证：失败测试检查日志路径和输出尾部；测试检查记录包含包、便携工具、SSH 块与备份路径。

## 6. 发布与分发

- [x] 6.1 配置 GoReleaser 六平台矩阵与 ldflags 版本注入。验证：本地或 CI 构建产物齐全，`version` 输出注入值。
- [x] 6.2 实现 `install.sh`、`install.ps1`、`install.cmd` 的下载、SHA256 校验与执行。验证：三平台冒烟测试；校验失败时中止且不执行。
- [x] 6.3 完善 README 的发布说明、SmartScreen/杀软指引与最低兼容版本声明。验证：文档中的下载与校验命令可照抄执行。

## 7. 集成验收

- [ ] 7.1 在 Windows 干净用户环境执行完整五步（或等价隔离环境）。验证：`verify --strict --json` 返回 ok，退出码 0，收据完整。
- [ ] 7.2 在 macOS/Linux CI 执行 plan dry-run 与单元测试矩阵。验证：CI 全部通过，无平台相关测试被跳过。
- [x] 7.3 复核三个 Open Question 的实测结论并回写 design.md。验证：Open Questions 要么有结论，要么明确转为后续任务。

## Workflow follow-up

- 由用户审查变更并人工执行 git commit。
- 归档后验证主 specs 正确生成。
