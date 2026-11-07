# ssh-auth-provisioning Specification

## Purpose
定义单账号 SSH 鉴权的准备、公钥登记、账号验证与错误恢复，确保三个仓库使用同一账号，且不会误用机器上其他账号的 key。

## Requirements

### Requirement: 可认证的现有 key 必须复用
安装器 SHALL 在生成新 key 前探测现有 key；若某把 key 能以目标账号通过 SSH 认证，MUST 复用它，避免制造多余凭据。

#### Scenario: 现有 key 认证为目标账号
- **WHEN** `ssh -T` 使用某把现有 key 返回 `Hi celestezj!`
- **THEN** 安装器复用该 key 并记录其指纹

#### Scenario: 没有匹配的 key
- **WHEN** 所有现有 key 都不能认证为目标账号
- **THEN** 安装器生成新的 ed25519 key

### Requirement: Host 别名必须安全写入
安装器 SHALL 使用带标记的 Host 块配置别名，写入前备份 `~/.ssh/config`，并设置 `IdentitiesOnly yes`；MUST NOT 覆盖已有的非标记配置块。

#### Scenario: 别名不存在
- **WHEN** `~/.ssh/config` 中没有对应别名
- **THEN** 安装器追加标记块，指向实际地址、专用 key 和 `IdentitiesOnly yes`

#### Scenario: 别名已被占用
- **WHEN** 已存在同名但内容不同的 Host 块
- **THEN** 安装器不改写该块，改用备用别名并记录选择

### Requirement: 远程地址必须使用别名替换 github.com
安装器 SHALL 将 clone、子模块和部署仓库地址规范化为 `git@<alias>:<owner>/<repo>.git`，其中 `github.com` 被别名替换。

#### Scenario: 源仓库地址改写
- **WHEN** 配置为 `git@github.com:celestezj/blog-delta.git`，别名为 `celestezj`
- **THEN** 实际 clone 使用 `git@celestezj:celestezj/blog-delta.git`

### Requirement: 公钥登记必须通过浏览器辅助完成
安装器 SHALL 生成 GitHub 预填公钥页面，打开浏览器，并轮询 `ssh -T` 直到返回 `Hi <owner>!` 或超时；MUST NOT 要求用户粘贴私钥。

#### Scenario: 用户在浏览器完成添加
- **WHEN** 用户在 GitHub 页面点击 Add SSH key 后返回终端
- **THEN** 安装器在下一次轮询中检测到 `Hi <owner>!` 并继续

#### Scenario: 轮询超时
- **WHEN** 超过配置的超时时间仍未认证成功
- **THEN** 安装器返回 `human_required`，提供重新打开页面或重建 key 的选项

### Requirement: 错误账号必须是确定性失败
安装器 SHALL 将 greeting 中的账号与目标 owner 比较；不一致时返回 `AUTH_WRONG_ACCOUNT`，MUST NOT 自动重试或自动删除 key。

#### Scenario: key 被添加到错误账号
- **WHEN** `ssh -T` 返回 `Hi muggledy!`，而目标为 `celestezj`
- **THEN** 安装器停止并提示用户删除错误账号上的 key，或重新生成新 key

### Requirement: 账号验证必须包含仓库权限证明
安装器 SHALL 在 greeting 之外验证源仓库和主题仓库可读，并通过 `git push --dry-run` 验证部署仓库可写；MUST NOT 仅凭 `ssh -T` 判定鉴权完成。

#### Scenario: greeting 正确但无部署权限
- **WHEN** SSH 认证为目标账号，但部署仓库 dry-run 被拒绝
- **THEN** 安装器报告授权不足并停止

#### Scenario: 读写探针全部通过
- **WHEN** 源仓库与主题仓库可读，部署仓库 dry-run 可写
- **THEN** 鉴权步骤通过

### Requirement: 密钥默认无口令并安全存储
安装器 SHALL 默认生成无口令的专用 key 以支持非交互流程，并将私钥权限收紧为仅当前用户可读写；私钥 MUST NOT 出现在日志、JSON 或浏览器中。

#### Scenario: 非交互模式生成 key
- **WHEN** AI 以非交互模式运行安装器
- **THEN** 生成的 key 无口令，且输出中只出现公钥指纹

### Requirement: 网络端点必须自动回退
安装器 SHALL 先尝试 `github.com:22`，连接失败时回退到 `ssh.github.com:443`，并将成功端点写入状态文件；该选择 MUST NOT 出现在用户配置里。

#### Scenario: 22 端口不可达
- **WHEN** 连接 `github.com:22` 超时
- **THEN** 安装器自动尝试 `ssh.github.com:443` 并继续验证

### Requirement: 浏览器与公钥处理必须防注入
安装器 SHALL 只打开 `https://github.com` 域下的 URL，并正确编码公钥参数；MUST NOT 通过 shell 拼接 URL 或命令。

#### Scenario: 公钥包含特殊字符
- **WHEN** 公钥内容需要放入查询参数
- **THEN** 安装器进行 URL 编码并通过参数数组打开页面
