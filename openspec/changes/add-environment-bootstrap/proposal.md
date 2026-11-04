# Proposal

## Why

全新机器可能缺少 git、Node.js，且私有博客仓库在克隆前无法凭据获取安装器。手工配置 SSH 账号、密钥、主题子模块和构建验证容易漏步，也容易被“命令没报错”误判为成功。需要一个可恢复、可验证、AI 与用户都能调用的引导安装器。

## What Changes

- 新增 Go 单文件 CLI `blog-bootstrap`，无运行时依赖，跨平台发布。
- 配置驱动：三个仓库 URL、commit 身份、可选 SSH 别名；强制三个仓库同 owner，部署仓库必须为 `<owner>.github.io`。
- 五步状态机：git + SSH 鉴权、Node.js、hexo（可选）、拉取博客仓库与主题子模块、构建与本地预览探测。
- 浏览器辅助公钥登记：打开 GitHub 预填页面，轮询 `ssh -T`，以 `Hi <owner>!` 作为账号验证；错误账号是确定性失败，不自动重试。
- 工具链 system-first 安装，失败回退项目内便携版；不覆盖系统 Node。
- 引入 plan/consent、状态文件与锁、strict verify、receipt、稳定退出码和 JSON 协议。
- 提供 AI 安装手册，并由 `agent-guide` 输出同一份内容。
- 新增 GitHub Releases 发布流水线；不包含任何发布或 deploy 行为。

## Capabilities

### New Capabilities

- `bootstrap-orchestration`: 配置解析与同 owner 校验、plan/consent、状态机、重试与恢复、strict verify、receipt、JSON 协议与 AI human handoff。
- `toolchain-provisioning`: system-first 安装 git、Node.js、hexo，失败回退便携版，版本探测与 PATH 处理。
- `ssh-auth-provisioning`: 复用或生成 SSH key、Host 别名、浏览器公钥登记、账号验证、错误账号处理。
- `blog-workspace-provisioning`: 拉取博客仓库、配置主题子模块、固定 SHA 校验、`npm ci`、构建产物与本地 server 冒烟。

### Modified Capabilities

- 无。

## Impact

- 新增独立公开仓库 `blog-bootstrap`，Go 1.24+，发布 Windows/macOS/Linux 的 amd64/arm64 二进制。
- 不修改 blog-delta 内容。blog-delta 的兼容性改造（相对子模块 URL、deploy owner 推导、`.node-version`、CI 版本源）属于另一个仓库的独立变更。
- 仅支持 SSH 鉴权；不涉及 CI secret 创建、Pages 设置或实际部署。
- 需要 GitHub Releases 作为公开分发渠道。
