# blog-workspace-provisioning Specification

## Purpose
定义博客仓库与主题子模块的拉取、固定提交校验、依赖安装和本地构建预览验证，确保安装结束时可重复构建。

## Requirements

### Requirement: 目标目录冲突时不得删除
安装器 SHALL 在目标目录已存在时检查其 remote 是否与配置的源仓库一致；不一致时返回 `REPO_CONFLICT` 并停止，MUST NOT 删除或覆盖该目录。

#### Scenario: 目录属于同一仓库
- **WHEN** 目标目录已是配置中的源仓库且 remote 匹配
- **THEN** 安装器跳过 clone，进入校验

#### Scenario: 目录属于其他仓库
- **WHEN** 目标目录存在但不是配置中的源仓库
- **THEN** 安装器停止并请用户更换目录或手动处理

### Requirement: 克隆必须使用配置的源仓库
安装器 SHALL 使用配置且经别名改写的源仓库地址克隆，并在完成后验证 remote、owner 与分支。

#### Scenario: 克隆成功
- **WHEN** 克隆完成且 remote 指向 `git@<alias>:<owner>/blog-delta.git`
- **THEN** 安装器验证目标分支并继续

#### Scenario: remote 不匹配
- **WHEN** 克隆后的 remote 与配置不一致
- **THEN** 安装器返回验证失败

### Requirement: 主题子模块必须按配置地址初始化
安装器 SHALL 在本地覆盖 `submodule.themes/A4.url` 后执行递归初始化，MUST NOT 修改 `.gitmodules`，并验证子模块已初始化且 HEAD 等于父仓库记录的 gitlink。

#### Scenario: 子模块初始化成功
- **WHEN** `git submodule status` 无前导 `-`，且主题 HEAD 等于 gitlink
- **THEN** 主题校验通过

#### Scenario: 子模块未初始化
- **WHEN** `git submodule status` 输出以 `-` 开头
- **THEN** 安装器重试初始化，仍失败则返回 `SUBMODULE_FAILED`

### Requirement: 无子模块声明时必须固定提交
当仓库没有声明主题子模块时，安装器 SHALL 使用配置的主题地址克隆，并 checkout 到 `git ls-tree HEAD themes/A4` 记录的固定提交。

#### Scenario: 独立主题克隆
- **WHEN** `.gitmodules` 中没有 themes/A4
- **THEN** 安装器克隆主题并检出父仓库记录的提交

#### Scenario: 提交不一致
- **WHEN** 主题 HEAD 与父仓库 gitlink 不一致
- **THEN** 安装器返回验证失败，不继续构建

### Requirement: 构建必须产出可验证文件
安装器 SHALL 依次执行 hexo clean、generate 和 `clean-search.js`，并要求 `public/index.html` 与 `public/search.json` 存在且非空。

#### Scenario: 构建成功
- **WHEN** 两个产物存在且大小大于零
- **THEN** 构建步骤通过

#### Scenario: 产物缺失
- **WHEN** `public/search.json` 不存在
- **THEN** 安装器返回 `BUILD_FAILED` 并附日志尾部

### Requirement: 本地预览必须使用受控 server
安装器 SHALL 在空闲端口启动临时 hexo server，轮询 HTTP 直到返回 200，然后回收整个进程树；MUST NOT 直接执行不会退出的 `npm run preview`。

#### Scenario: 端口被占用
- **WHEN** 首选端口已被占用
- **THEN** 安装器选择另一个空闲端口并重试

#### Scenario: 验证完成
- **WHEN** 本地服务返回 HTTP 200
- **THEN** 安装器记录状态并终止 server 进程树

### Requirement: 失败必须保留可诊断日志
安装器 SHALL 捕获关键步骤的输出尾部，写入本地日志文件并在错误响应中给出路径；日志 MUST NOT 包含私钥或其他密钥。

#### Scenario: npm 安装失败
- **WHEN** `npm ci` 返回非零
- **THEN** 安装器返回 `NPM_INSTALL_FAILED`，附带日志路径与输出尾部

### Requirement: 安装改动必须可人工回滚
安装器 SHALL 记录本次安装修改的包、便携工具、SSH 配置块和备份路径，写入 `changes.json`，供用户手动撤销。

#### Scenario: 查看安装记录
- **WHEN** 用户需要回滚
- **THEN** `changes.json` 能列出所有被修改的位置和对应备份
