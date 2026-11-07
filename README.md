# blog-bootstrap

把一台新机器准备到能构建、预览 Hexo 博客的引导安装器。

## 它不是什么

- 不是 Bootstrap CSS。
- 不是某个平台或服务。
- 不包含博客内容，不保存任何密钥。

“bootstrap” 在这里是动词：把空环境引导到可工作状态。程序本身是自包含的 Go 单文件，分发渠道是 GitHub 仓库 + Releases。

## 它做什么

按顺序执行五步，每一步都有独立的探测与验证：

1. git + SSH 鉴权
2. Node.js
3. hexo CLI（可选）
4. 拉取博客仓库与主题子模块
5. `npm ci` + 构建 + 本地 server 冒烟

只有下面这条命令返回 `"ok": true` 且退出码为 0，才算安装完成：

```bash
blog-bootstrap verify --strict --json
```

## 文档

- [使用手册](docs/usage.md)：配置、标准流程、`--json`/`--strict`/consent hash 的含义、中断续跑与常见问题。
- [架构与实现原理](docs/architecture.md)：包结构、命令数据流、状态机、锁、失败模型与平台适配。
- [AI 安装手册](docs/agent-install.md)：AI 必须遵守的硬规则；`blog-bootstrap agent-guide` 输出同一份内容。

## 状态

v0.1.0 已发布：五步逻辑、CLI 协议、发布脚本与三平台 CI 均已验证。

## 常用命令

```bash
blog-bootstrap plan --json                    # 输出改动计划与 consent hash
blog-bootstrap run --consent <hash> --json    # 执行已确认的计划
blog-bootstrap verify --strict --json         # 现场重跑全部探针（唯一成功信号）
blog-bootstrap verify --step auth.ssh --json  # 只重跑一个步骤
blog-bootstrap status --json
blog-bootstrap next --json
blog-bootstrap doctor --json
blog-bootstrap agent-guide
```

所有命令都支持 `--json`，输出带 `schema_version: 1` 的稳定协议；退出码为 0 成功、2 需人工、3 可重试、4 致命、5 验证失败。为什么安装分成 plan/run/verify 三步、`--json` 与 `--strict` 的含义、consent hash 如何生成，见[使用手册](docs/usage.md#4-四个必须先理解的概念)。

## 配置

复制 `blog-setup.example.yml` 为 `blog-setup.yml`，填写三个仓库地址与 commit 身份。`blog-setup.yml` 不提交。

默认读取**当前工作目录**下的 `blog-setup.yml`（不是 exe 所在目录），可用 `--config <path>` 覆盖；配置中的相对路径按配置文件所在目录解析。字段与默认值详见[使用手册](docs/usage.md#3-配置文件)。

| 字段 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `version` | 是 | 无 | 配置版本，当前只支持 `1` |
| `repos.source` | 是 | 无 | 博客源码仓库，规范 GitHub SSH/HTTPS 地址 |
| `repos.theme` | 是 | 无 | Hexo 主题仓库 |
| `repos.deploy` | 是 | 无 | 部署仓库，必须精确命名为 `<account>.github.io` |
| `git_identity.name` | 是 | 无 | commit 作者名；不修改已有全局 git 身份 |
| `git_identity.email` | 是 | 无 | commit 作者邮箱；不修改已有全局 git 身份 |
| `auth.alias` | 否 | 账号名 | SSH Host 别名，远程地址中的 `github.com` 会被替换 |
| `auth.key` | 否 | `~/.ssh/id_ed25519_<account>` | 专用私钥路径；公钥指纹会出现在 JSON 中，私钥不会 |
| `toolchain.strategy` | 否 | `system_first` | `system_first` 或 `isolated` |
| `toolchain.node_version` | 否 | `22` | clone 后以仓库 `.node-version` 为准 |
| `workspace.dir` | 否 | 源仓库名 | 目标目录；相对路径按配置文件所在目录解析 |
| `workspace.branch` | 否 | `master` | 博客仓库分支 |

硬规则：

- 三个仓库必须属于同一个 GitHub 账号；
- 部署仓库必须精确命名为 `<account>.github.io`；
- 只支持 SSH 鉴权；
- `git_identity` 只影响 commit 作者，与 SSH 账号无关；本机已有全局身份时不会被覆盖。
- 目标目录已存在但不是同一个仓库时，安装器停止，不删除或覆盖。

## 构建

需要 Go 1.24+，无需 CGO。

macOS / Linux：

```bash
go build -o blog-bootstrap ./cmd/blog-bootstrap
```

Windows（PowerShell 或 cmd）：

```powershell
go build -o blog-bootstrap.exe ./cmd/blog-bootstrap
```

本地构建的版本号默认为 `dev`。需要模拟发布版本时，用 ldflags 注入（与 GoReleaser 一致）：

```bash
go build -ldflags "-X main.version=v0.1.0" -o blog-bootstrap.exe ./cmd/blog-bootstrap
```

发布时由 CI 交叉编译 Windows/macOS/Linux 的 amd64/arm64，并通过 GitHub Releases 分发。

## 安装

从 GitHub Releases 下载对应平台归档，或使用仓库中的引导脚本：

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

也可以运行仓库中的 `install.cmd`（与 `install.ps1` 放在同一目录）。

脚本会下载归档与 `checksums.txt`，校验 SHA256 后才解压并执行；校验失败时立即中止，不执行任何二进制。可用环境变量覆盖行为：

| 变量 | 作用 |
|---|---|
| `BLOG_BOOTSTRAP_VERSION` | 指定 tag，例如 `v0.1.0`；省略时取 latest |
| `BLOG_BOOTSTRAP_BASE_URL` | 覆盖仓库地址（镜像/本地测试） |
| `BLOG_BOOTSTRAP_INSTALL_DIR` | 安装目录 |

手动下载归档时，同样先核对 `checksums.txt` 中的 SHA256，再运行其中的 `blog-bootstrap`。

### SmartScreen 与杀软

二进制未做代码签名，Windows 首次运行可能显示 SmartScreen 警告：确认文件名与 SHA256 后，点击“更多信息”→“仍要运行”。若杀软误报，请核对校验和并将文件提交给厂商分析；不要关闭杀软或添加宽泛的排除规则。

### 最低兼容版本

- Windows 10 1809+（amd64/arm64），PowerShell 5.1；
- macOS 11+（Intel/Apple Silicon）；
- 主流 Linux 发行版（amd64/arm64），需要 glibc 2.17+ 或兼容环境。

运行不依赖 Go；git 与 Node.js 缺失时由安装器按 plan 安装或下载便携版本。`blog-bootstrap version` 输出发布时通过 ldflags 注入的版本号。

## 发版（维护者）

发布由 `.github/workflows/release.yml` 在推送 `v*` tag 时触发，使用 GoReleaser 交叉编译六平台产物：

1. 确认 `main` 的 CI（`.github/workflows/ci.yml`）三平台全绿。
2. 创建并推送 tag。tag 一旦发布不要再移动或复用：

   ```bash
   git tag -a v0.1.0 -m "v0.1.0: first working bootstrap"
   git push origin v0.1.0
   ```

3. 等 `release` workflow 完成，在 https://github.com/celestezj/blog-bootstrap/releases 核对以下产物：
   - `blog-bootstrap_<版本>_windows_amd64.zip`、`windows_arm64.zip`
   - `blog-bootstrap_<版本>_darwin_amd64.tar.gz`、`darwin_arm64.tar.gz`
   - `blog-bootstrap_<版本>_linux_amd64.tar.gz`、`linux_arm64.tar.gz`
   - `checksums.txt`
4. 发布成功后，`install.sh` / `install.ps1` 默认取 latest，会自动指向这个版本。

注意：`ci.yml` 只跑测试，不产生可下载产物；只有推送 `v` 前缀的 tag 才会创建 Release。

## 目录

```text
cmd/blog-bootstrap/   程序入口
internal/             配置、状态机、探测、平台适配
docs/                 使用手册、架构文档与 AI 安装手册
scripts/              CI/文档一致性脚本
install.sh / install.ps1 / install.cmd
.goreleaser.yaml      六平台发布配置
blog-setup.example.yml
```
