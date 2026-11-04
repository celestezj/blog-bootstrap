# Spec Delta

## Purpose

定义 git、Node.js、hexo 的探测、安装、回退与版本验证行为，保证不同系统上都能得到可用且可复现的构建工具链。

## ADDED Requirements

### Requirement: 已有工具必须优先复用
安装器 SHALL 先探测系统已有的 git 与 Node.js；版本满足要求时直接复用，MUST NOT 重复安装或覆盖。

#### Scenario: 已有可用 Node
- **WHEN** 系统 `node -v` 为 22 且满足版本要求
- **THEN** 安装器记录该路径并跳过 Node 安装

#### Scenario: 已有版本过低
- **WHEN** 系统 Node 版本低于支持区间
- **THEN** 安装器不覆盖系统 Node，改为准备便携版本

### Requirement: 工具安装必须系统优先并可回退
安装器 SHALL 优先使用系统包管理器安装 git 和 Node.js；包管理器不可用、无权限或安装失败时，MUST 回退到项目内便携版本。

#### Scenario: 包管理器可用
- **WHEN** Windows 上 winget 可用且用户同意计划
- **THEN** 安装器通过 winget 安装所需工具并验证版本

#### Scenario: 无管理员权限
- **WHEN** 系统安装需要提权且当前进程无法提权
- **THEN** 安装器回退到便携版本，不中止整个流程

### Requirement: 便携工具必须隔离并校验
安装器 SHALL 将便携版 git 和 Node.js 放在 `.bootstrap/tools/` 下，下载后校验 SHA256，并使用绝对路径调用。

#### Scenario: 校验通过
- **WHEN** 下载的便携 Node 包哈希与官方校验值一致
- **THEN** 安装器解压并将其路径写入状态文件

#### Scenario: 校验失败
- **WHEN** 下载包的 SHA256 不匹配
- **THEN** 安装器删除该文件并返回可重试失败，不执行该二进制

### Requirement: Node 版本必须以仓库声明为准
安装器 SHALL 在 clone 后读取博客仓库的 `.node-version`；若当前 Node 不匹配，MUST 使用项目内便携 Node，而不是替换系统 Node。

#### Scenario: 版本一致
- **WHEN** 已安装 Node 与 `.node-version` 一致
- **THEN** 安装器继续使用该 Node

#### Scenario: 版本不一致
- **WHEN** 系统 Node 为 22，而 `.node-version` 要求 20
- **THEN** 安装器在 `.bootstrap/tools/node` 准备 20，并让后续命令使用它

### Requirement: hexo CLI 是可选步骤
安装器 SHALL 尝试安装全局 `hexo-cli`，但安装失败 MUST NOT 阻断构建链；构建和验收使用项目内 hexo。

#### Scenario: 全局安装失败
- **WHEN** 全局 npm 前缀不可写
- **THEN** 安装器记录警告并继续执行后续步骤

#### Scenario: 构建使用本地 hexo
- **WHEN** 执行构建步骤
- **THEN** 安装器调用项目 `node_modules` 中的 hexo，而不是依赖全局命令

### Requirement: 依赖安装必须可复现
安装器 SHALL 在存在 lockfile 时使用 `npm ci`，并以 lockfile 哈希与 Node 版本作为缓存键；MUST NOT 在无明确需要时改用 `npm install`。

#### Scenario: 缓存命中
- **WHEN** lockfile 哈希和 Node 版本与上次成功记录一致
- **THEN** 安装器跳过重复安装并直接进入构建验证

#### Scenario: lockfile 不一致
- **WHEN** lockfile 哈希发生变化
- **THEN** 安装器重新执行 `npm ci`

### Requirement: 子进程必须使用确定的运行环境
安装器 SHALL 使用状态文件中记录的绝对路径启动 git、node、npm 和 hexo，并为子进程设置所需 PATH，MUST NOT 依赖当前终端已刷新的 PATH。

#### Scenario: 安装后当前 PATH 未刷新
- **WHEN** 工具刚刚安装但当前进程 PATH 尚未更新
- **THEN** 安装器仍能通过绝对路径成功调用工具

### Requirement: 权限与非交互场景必须显式处理
安装器 SHALL 在需要提权或交互、但当前为非交互环境时返回 `human_required`，MUST NOT 挂起等待输入。

#### Scenario: 非 TTY 下需要 sudo
- **WHEN** AI 在无 TTY 环境运行且安装需要 sudo 密码
- **THEN** 安装器返回退出码 2，并给出用户需要手动执行的命令
