# Spec Delta

## Purpose

定义安装器的配置校验、改动授权、步骤状态机、验证与恢复行为，使 AI 和用户都能得到可重复、不可伪造的安装结果。

## ADDED Requirements

### Requirement: 三个仓库必须属于同一 owner
安装器 SHALL 从 source、theme、deploy 三个仓库 URL 解析 owner，并在三者不一致时拒绝继续。

#### Scenario: owner 一致
- **WHEN** 三个 URL 的 owner 均为 `celestezj`
- **THEN** 预检通过并进入 plan 阶段

#### Scenario: owner 不一致
- **WHEN** source 属于 `alice`，deploy 属于 `bob`
- **THEN** 安装器返回 `CONFIG_OWNER_MISMATCH` 并停止，不写入任何运行状态

### Requirement: 部署仓库必须使用用户站点命名
安装器 MUST 要求 deploy 仓库精确命名为 `<owner>.github.io`，否则拒绝继续。

#### Scenario: 合法部署仓库
- **WHEN** owner 为 `celestezj`，deploy 为 `celestezj/celestezj.github.io`
- **THEN** 预检通过

#### Scenario: 非法部署仓库
- **WHEN** deploy 为 `celestezj/blog-pages`
- **THEN** 安装器返回 `DEPLOY_REPO_NAME_INVALID` 并停止

### Requirement: 只接受规范化的 GitHub 仓库地址
安装器 SHALL 只接受 `git@github.com:owner/repo.git` 或 `https://github.com/owner/repo.git`，并拒绝带自定义 host 别名的地址。

#### Scenario: 规范化 SSH 地址
- **WHEN** 配置使用 `git@github.com:celestezj/blog-delta.git`
- **THEN** 安装器解析出 owner `celestezj` 和仓库 `blog-delta`

#### Scenario: 别名地址被拒绝
- **WHEN** 配置使用 `git@celestezj:celestezj/blog-delta.git`
- **THEN** 安装器拒绝该配置并提示改用规范化地址

### Requirement: 可选配置必须有确定的默认值
安装器 SHALL 在可选字段缺失时使用约定默认值：SSH 别名取 owner，key 路径取 `~/.ssh/id_ed25519_<owner>`，目标目录取源仓库名，分支取 `master`。

#### Scenario: 省略 alias
- **WHEN** 配置没有 `auth.alias`
- **THEN** 安装器使用解析出的 owner 作为 SSH 别名

#### Scenario: 显式 alias 覆盖默认值
- **WHEN** 配置写有 `auth.alias: celestezj-blog`
- **THEN** 安装器使用 `celestezj-blog` 生成远程地址和 Host 配置

### Requirement: 改动前必须输出计划并绑定同意哈希
安装器 SHALL 在修改系统前输出 plan，列出将安装的包、将写入或备份的文件、将创建的 key 与别名、以及将要更新的用户级 PATH 条目，并要求 `run` 携带与当前 plan 完全一致的同意哈希。

#### Scenario: 计划未变化
- **WHEN** 用户确认 plan 后传入相同的同意哈希
- **THEN** 安装器开始执行步骤

#### Scenario: 计划已变化
- **WHEN** 配置或目标环境变化导致 plan 哈希不同
- **THEN** 安装器拒绝执行并要求重新确认计划

### Requirement: 只有严格验证可以判定成功
安装器 SHALL 仅以 `verify --strict` 的实时探针结果判定成功；detect、apply 和缓存状态文件 MUST NOT 被当作成功证据。

#### Scenario: 缓存显示完成但探针失败
- **WHEN** 状态文件记录某步骤完成，但 strict verify 的探针失败
- **THEN** 安装器返回验证失败，并将下游步骤标记为失效

#### Scenario: 全部必需步骤通过
- **WHEN** 所有必需步骤的 strict 探针均通过
- **THEN** 安装器写入 receipt，并返回退出码 0

### Requirement: 失败必须区分可重试与确定性失败
安装器 SHALL 对网络类失败做有上限的退避重试，对配置错误、错误账号、目录冲突等确定性失败立即停止，并支持从最后一个已验证步骤恢复。

#### Scenario: 网络失败
- **WHEN** 下载依赖连续失败且网络错误可重试
- **THEN** 安装器最多重试三次，仍失败则转为人工处理

#### Scenario: 错误账号
- **WHEN** SSH 探针返回 `Hi <其他账号>!`
- **THEN** 安装器返回 `AUTH_WRONG_ACCOUNT`，不自动重试

#### Scenario: 从中断处恢复
- **WHEN** 用户再次运行且前序步骤仍通过 strict 探针
- **THEN** 安装器跳过已验证步骤，从第一个失败步骤继续

### Requirement: 同一时间只允许一个安装实例
安装器 SHALL 使用带 PID 的锁文件阻止并发执行，并在进程异常退出后允许安全接管。

#### Scenario: 并发运行
- **WHEN** 第二个安装进程发现锁已被活动进程持有
- **THEN** 它拒绝执行并报告持锁 PID

### Requirement: 机器可读协议必须稳定
安装器 SHALL 在所有命令中提供带 `schema_version` 的 JSON 输出，并使用固定退出码：0 成功、2 需人工、3 可重试、4 致命、5 验证失败。

#### Scenario: 需要人工操作
- **WHEN** 公钥尚未添加到 GitHub
- **THEN** 安装器返回退出码 2，并在 `next_action` 中给出 `human` 类型、提示、URL 与轮询参数

#### Scenario: JSON 中不得出现私钥
- **WHEN** 任何命令输出 JSON
- **THEN** 输出中只包含公钥指纹，不包含私钥内容

### Requirement: AI 手册必须与 CLI 行为一致
安装器 SHALL 通过 `agent-guide` 输出与仓库内 `docs/agent-install.md` 相同的内容，并在 CI 中校验两者一致。

#### Scenario: 文档漂移
- **WHEN** 手册与 `agent-guide` 的输出不一致
- **THEN** CI 失败并要求同步更新
