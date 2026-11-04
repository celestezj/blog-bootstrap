# AI 安装手册

> `blog-bootstrap agent-guide` 输出本文件；CI 会校验两者一致。

## 0. 成功定义

只有下面这条命令返回 `"ok": true` 且退出码为 0，才算安装完成：

```bash
blog-bootstrap verify --strict --json
```

日志、状态文件、单个步骤退出码为 0，都不构成成功证据。

## 1. 绝对禁止

1. 未通过 strict verify 时，不得向用户报告“安装完成”。
2. 不得使用 `--force` 绕过任何步骤。
3. 不得手动编辑 `.bootstrap/state.json`，不得伪造收据。
4. 不得读取、打印、复制 `~/.ssh` 下的私钥；只能使用工具输出的公钥指纹。
5. 不得修改全局 `git config --global user.name/user.email`。工具只在缺失时按配置写入；已有值必须保留。
6. 不得执行 `git commit`、`git push`、`deploy.sh`。安装流程不包含发布。
7. 不得删除目标目录。目录冲突时停止并询问用户。
8. 不得直接执行 `npm run preview`，它会一直运行。验收使用工具管理的构建 + 临时 server。

## 2. 下载与启动

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

脚本从 GitHub Releases 下载归档与 `checksums.txt`，校验 SHA256 通过后才解压并执行；失败即中止。手动下载时同样先核对 `checksums.txt`。安装位置可用 `BLOG_BOOTSTRAP_INSTALL_DIR` 覆盖，版本可用 `BLOG_BOOTSTRAP_VERSION=vX.Y.Z` 固定。

## 3. 命令协议

| 命令 | 用途 |
|---|---|
| `plan --json` | 解析配置、校验同 owner、输出改动计划和 plan hash |
| `run --consent <hash> --json` | 执行已确认的计划 |
| `status --json` | 查看步骤状态（缓存，不作为成功证据） |
| `next --json` | 返回下一步及 `next_action` |
| `verify --strict --json` | 现场重跑全部探针；唯一成功信号 |
| `doctor --json` | 安装后的健康检查 |
| `agent-guide` | 输出本手册 |

退出码：

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 2 | 需要人工 |
| 3 | 可重试 |
| 4 | 致命错误 |
| 5 | 验证失败 |

### next_action 形状

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

`type` 的取值：`run`、`human`、`retry`、`abort`。

## 4. human_required 的处理

1. 把 `message` 原样转述给用户。
2. 如果 `open_url` 存在，请用户打开；AI 不代替用户点击。
3. 等待用户明确表示已完成，或执行 `poll.command`，直到 `timeout_seconds`。
4. 再次执行 `verify --step <id>`，最终以 strict verify 为准。
5. 绝不模拟用户确认，绝不假设浏览器操作已经成功。

## 5. 错误码与回退

| code | 含义 | 动作 |
|---|---|---|
| `CONFIG_OWNER_MISMATCH` | 三个仓库 owner 不一致 | 停止，要求用户修正配置 |
| `DEPLOY_REPO_NAME_INVALID` | 部署仓库不是 `<owner>.github.io` | 停止 |
| `TOOLS_MISSING` | git/node 缺失 | 按计划安装；失败转人工 |
| `NETWORK_UNREACHABLE` | GitHub 或 nodejs.org 不可达 | 重试 3 次，再转人工 |
| `AUTH_KEY_NOT_ADDED` | 公钥尚未添加到账号 | 轮询；超时后询问是否重建 key |
| `AUTH_WRONG_ACCOUNT` | greeting 返回了别的账号 | 停止；让用户删除错误账号上的 key 或重新生成 key |
| `REPO_CONFLICT` | 目标目录已存在且不是同一仓库 | 停止，不删除 |
| `SUBMODULE_FAILED` | 主题子模块拉取或校验失败 | 重试一次，再转人工 |
| `NPM_INSTALL_FAILED` | `npm ci` 失败 | 附日志尾部，转人工 |
| `BUILD_FAILED` | hexo 构建失败 | 附日志尾部，转人工 |
| `PREVIEW_TIMEOUT` / `PREVIEW_FAILED` | 本地 server 未返回 200 | 换端口重试，再转人工 |
| `CONSENT_MISMATCH` | plan 已变化或同意哈希不匹配 | 重新展示 plan 并取得同意 |
| `VERSION_MISMATCH` | 本地 Node 与 `.node-version` 不一致 | 使用项目内便携 Node，不替换系统 Node |

`CONFIG_OWNER_MISMATCH`、`AUTH_WRONG_ACCOUNT`、`REPO_CONFLICT` 是确定性失败，不得自动重试。

## 6. 五步的验收探针

| 步骤 | 通过条件 |
|---|---|
| git | `git --version` 达标；`ssh -T` 输出 `Hi <account>!`；源仓库可读；部署仓库 `push --dry-run` 通过 |
| node | `node -v` 与 `npm -v` 在支持的版本区间 |
| hexo | 全局 `hexo-cli` 可执行并返回版本号（可选步骤；构建使用项目内 hexo） |
| clone | remote 匹配配置；分支正确；`git submodule status` 无前导 `-`；主题 HEAD 等于 gitlink |
| preview | `public/index.html` 与 `public/search.json` 存在；本地 server 返回 HTTP 200；进程已回收 |

## 7. 汇报模板

成功时向用户汇报：

- git / node / npm / hexo 版本；
- SSH greeting 结果；
- 三个仓库的读/写探针结果；
- 构建产物与本地 200 状态；
- 日志路径；
- 明确说明：未执行 commit、push、deploy。

失败时向用户汇报：

- 当前步骤；
- 错误码；
- 已完成和未完成的步骤；
- 用户需要执行的动作；
- 日志路径。

## 8. 安全边界

- 仅支持 SSH 鉴权。
- 只把公钥放进浏览器页面，私钥不离开本机。
- 所有外部命令通过 argv 调用，不经过 shell。
- 不收集遥测；只访问 GitHub、nodejs.org 和系统包管理器。
- plan 是唯一的改动授权。AI 必须先把 plan 展示给用户，取得同意后再传 plan hash。
