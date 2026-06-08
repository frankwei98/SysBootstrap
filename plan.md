# sys-bootstrap 当前产品定义与后续计划

## 1. 产品目标

`sys-bootstrap` 是个人 Linux VM 开荒工具，用于替代早期 OneLineSetup Bash MVP。

当前产品目标：

- 使用 Go 编译为单文件二进制。
- 使用 `charmbracelet/huh` 提供交互式 CLI。
- 支持 one-liner 启动。
- 支持 `doctor`、`plan`、`run`、单模块执行、`config`、`uninstall` 和版本输出。
- 支持 Debian / Ubuntu 及其他 apt 系 Linux 发行版。
- Go CLI 作为主入口；旧 Bash 方案只作为历史背景保留，不再要求兼容执行入口。

项目命名统一为 `sys-bootstrap`：

- README 主名使用 `sys-bootstrap`。
- Release artifact 使用 `sys-bootstrap_*`。
- installer 文案使用 `sys-bootstrap`。
- 旧 OneLineSetup 仅在 Legacy/历史说明中简要提及。

## 2. Phase 1 范围

Phase 1 覆盖当前仓库已有模块能力：

- `base`
- `ssh`
- `node`
- `ai`
- `user`
- `ssh_keygen`

当前 `gum` 模块废弃，不再作为 Go 版本模块保留，因为交互层由 Huh 接管。

不在当前范围内：

- docker
- timezone
- fail2ban
- Bubble Tea 复杂 TUI
- 完整 profile/config 系统
- 面向大批量机器的声明式部署入口

当前允许的最小持久配置：

- CLI 语言
- APT mirror 选择

允许参考 Bash MVP 逻辑，并在 Go 版本中重写、修复幂等性和安全问题。当前不要求保留 Bash MVP 的可执行兼容入口。

## 3. CLI 行为

二进制名：`sys-bootstrap`

当前命令集合：

```bash
sys-bootstrap
sys-bootstrap run
sys-bootstrap plan
sys-bootstrap plan --json
sys-bootstrap doctor
sys-bootstrap module <id>
sys-bootstrap uninstall
sys-bootstrap config
sys-bootstrap config language <lang>
sys-bootstrap config apt-mirror <mirror>
sys-bootstrap version
```

行为定义：

- `sys-bootstrap` 无参数时，先执行 `doctor` 检查并展示结果，然后进入主菜单：
  - `provisioning`
  - `settings`
  - `exit`
- `sys-bootstrap run` 进入 Huh 交互执行流程。
- `sys-bootstrap plan` 只输出计划，不修改系统。
- `sys-bootstrap doctor` 检查环境，不修改系统。
- `sys-bootstrap module <id>` 执行单个模块。
- `sys-bootstrap uninstall` 卸载用户级工具并清理相关 shell 配置。
- `sys-bootstrap config` 管理语言和 APT mirror 设置。
- `sys-bootstrap version` 输出版本、commit、build date、Go 版本和平台信息。

`run` 行为：

- 先选择运行模式：
  - `user`
  - `full`
- `user` 模式只暴露用户级模块：
  - `node`
  - `ai`
  - `ssh_keygen`
- `full` 模式会自动强制包含 `base`，且 `base` 永远最先执行。
- `full` 模式下，用户可再选择是否执行：
  - `ssh`
  - `node`
  - `ai`
  - `user`
  - `ssh_keygen`
- 如果选择 `ai`，自动加入 `node`。
- `ai` 默认安装 Claude Code 和 Codex。

`module <id>` 行为：

- 如果模块有依赖缺失，提示用户确认是否补跑依赖。
- 用户拒绝补跑依赖时，中止并说明原因。
- 不静默自动补依赖。
- 对需要额外输入的模块，要求交互式终端。
- `ai` 在 non-interactive 情况下默认安装 Claude Code 和 Codex。

当前 non-interactive 支持范围：

- `doctor`
- `plan`
- `config language <lang>`
- `config apt-mirror <mirror>`
- 无需额外输入的模块
- `ai` 模块可使用默认选择执行
- `run` 仍按交互式流程设计，不作为完整 non-interactive 入口
- 不引入独立的声明式批量部署工作流

## 4. 模块行为

### base

目标：

- apt update / upgrade
- 安装基础包
- 安装 zellij

基础包：

```text
sudo
zsh
gnupg
apt-transport-https
git
curl
wget
unzip
tree
neovim
```

当前行为：

- 检查全部基础包和 zellij 是否都已满足。
- 若全部满足则整体跳过。
- 若未完全满足，则重新执行 `apt-get install` 基础包列表。
- zellij 单独检测并在已存在时跳过。
- 可选支持切换 APT mirror 到 CERNET，并在切换后用 `apt-get update` 验证；失败则回滚。

### ssh

默认端口：`22122`

当前 hardening 能力：

- 修改 SSH 端口
- 检查并在需要时安装 OpenSSH server
- 写入 `authorized_keys`
- 可选禁用 root login
- 可选禁用 password login
- 若检测到 active ufw，可询问是否放行新端口

要求：

- 修改 `/etc/ssh/sshd_config` 前必须备份。
- 修改后必须运行 `sshd -t`。
- 校验失败必须回滚。
- 重启服务时兼容 `ssh` 和 `sshd` service 名称。
- 完成后提示用户测试新端口连接。

### node

目标：

- 安装 nvm
- 安装 Node.js LTS
- 安装 pnpm
- 安装 bun

当前行为：

- 已安装则跳过。
- 写入 shell rc 文件，确保 nvm / bun 路径生效。
- bun 通过下载 release 资产并校验 SHA256 安装。
- 安装失败时优先返回失败工具名，并在可用时附带 stderr 摘要。

### ai

默认选择：

- Claude Code
- Codex

行为：

- 依赖 `node`
- 优先使用 pnpm
- 如果未检测到 pnpm，会明确告警后 fallback 到 npm
- Claude Code 包名：`@anthropic-ai/claude-code`
- Codex 包名：`@openai/codex`
- 对 Claude Code 保留 pnpm 场景下的 postinstall repair 逻辑

### user

行为：

- Huh 收集用户名。
- Huh 收集默认 shell：`bash` 或 `zsh`。
- Huh 收集是否加入 sudo 组。
- 若加入 sudo，额外收集是否启用 passwordless sudo。
- 公钥输入支持：
  - 手动粘贴
  - GitHub username 拉取 public keys

密码处理：

- 如果用户加入 sudo 且未启用 passwordless sudo，当前流程会交互执行 `passwd <user>`。
- 如果未加入 sudo，则不自动设置密码，只提示用户手动运行 `passwd <user>`。

当前要求：

- 用户已存在时不重复创建，直接补充 sudo 组、sudoers 和 authorized_keys 等选择的补充操作。
- 公钥格式需要校验。
- `.ssh` 和 `authorized_keys` 权限必须正确。
- passwordless sudo 写入 `/etc/sudoers.d/sys-bootstrap-<user>`，并在可用时通过 `visudo -cf` 校验。

### ssh_keygen

行为：

- 支持 ed25519 和 rsa。
- 默认 ed25519。
- 支持输入 comment。
- 检测已有密钥时询问是否覆盖。

要求：

- 默认不设置 passphrase。
- 输出私钥和公钥路径。
- 打印公钥内容。

## 5. Huh 交互流程

`sys-bootstrap`：

1. 执行 doctor。
2. 展示检查结果。
3. 若存在 fatal incompatibility，直接退出。
4. 若可继续，则进入主菜单：
   - provisioning
   - settings
   - exit

`sys-bootstrap run`：

1. 选择运行模式：`user` / `full`。
2. `user` 模式显示用户级模块选择。
3. `full` 模式显示完整初始化模块选择，并强制包含 `base`。
4. 若选择 `ai`，自动加入 `node`。
5. 收集各模块所需输入。
6. 展示执行计划。
7. 用户确认后执行。

UI 原则：

- 不使用 Gum。
- 不引入 Bubble Tea。
- 不做复杂实时 TUI。
- 保持表单短而直接。

## 6. Plan / Doctor / 日志

### plan

`plan` 支持文本和 JSON：

```bash
sys-bootstrap plan
sys-bootstrap plan --json
```

当前定义：

- `plan` 是全模块能力预览，不是一次真实 `run` 的精确预演。
- 直接执行 `plan` 时，按注册顺序遍历全部模块，并结合默认值、系统状态和已保存设置生成结果。
- 依赖关系主要通过解析后的模块顺序隐含体现，不单独输出完整依赖图。

输出内容：

- 模块顺序
- 每个模块将执行的高层步骤
- 当前环境检查结果带来的状态：
  - `satisfied`
  - `pending`
  - `error`
- warning / risk 提示
- summary

`plan` 不要求精确列出每一条 shell 命令，也不模拟全部安装逻辑。

### doctor

检查项：

- OS ID / version
- 是否为支持的 apt 系发行版
- CPU arch
- root 状态
- systemd
- apt-get
- bash
- curl
- network 基础可用性
- sshd
- ssh/sshd service 可用性

当前行为：

- Debian 11+ / Ubuntu 22+ 视为 primary supported。
- 其他 apt 系发行版可继续使用，但作为兼容路径提示。

exit code：

- fatal 不兼容时返回非零
- warning 仍返回 0

### 日志

终端输出策略：

- 默认显示模块级进度日志，不做完全静默模式。
- 显示模块开始、完成、跳过、失败。
- 在模块内部按需要输出关键步骤日志。
- 失败时尽量带上 stderr 或 exit code 摘要，但当前尚未完全统一为单一错误格式。

日志文件：

- 同时写入 `~/.local/state/sys-bootstrap/logs`
- root via sudo 时写入 invoking user 可访问的 state 目录
- 不写 `/var/log`

## 7. 权限与安装器

### 权限

不要程序启动即强制 root。

当前规则：

- `doctor`、`plan`、`config` 可普通用户运行。
- `run` 在 `user` 模式下可普通用户运行。
- `run` 在 `full` 模式下会因为自动包含 `base` 而要求 root；非 root 时提示用户手动用 sudo 重跑。
- `module` 在执行需要 root 的模块前检查权限。
- 不做自动 sudo re-exec。
- 若直接以 root 身份执行用户级模块且不是通过 sudo 带入 invoking user，额外给出确认提示。

### installer

one-liner 当前默认交互询问：

- language
- download region
- run mode
- APT mirror
- 临时运行或安装到 `/usr/local/bin`

installer 可以安装最小依赖：

- apt 系发行版可自动安装 `curl`、`ca-certificates` 等下载二进制所需依赖

installer 当前职责：

- 检查 OS / arch
- 必要时安装最小下载依赖
- 解析最新 release 版本
- 从 GitHub Release 下载匹配二进制
- 中国大陆场景下优先尝试镜像 / 代理 CDN
- 在可用时校验 SHA256；如果无法获取 checksum 或本地无 SHA256 工具，则明确提示并要求用户确认
- `chmod`
- 临时运行或安装到 `/usr/local/bin`
- 通过环境变量把 language / run mode / APT mirror 等选择传给二进制

## 8. 发布与 CI

GitHub Release artifact 命名：

```text
sys-bootstrap_linux_amd64
sys-bootstrap_linux_arm64
```

Release workflow：

- tag push（`v*`）触发构建和发布
- `CGO_ENABLED=0`
- 至少构建：
  - linux-amd64
  - linux-arm64

Checks workflow：

- 需要
- 至少运行：

```bash
go test ./...
go build -o sys-bootstrap ./cmd/sys-bootstrap/
```

Shellcheck 不作为当前必须项。

## 9. 当前 Go 结构

当前主要目录：

```text
cmd/
  sys-bootstrap/
    main.go

internal/
  app/
    app.go
    plan.go
    rootcheck.go
    runner.go
    uninstall.go
    version.go

  cli/
    commands.go
    runmode.go

  ui/
    forms.go

  system/
    aptmirror.go
    command.go
    context.go

  modules/
    module.go
    registry.go
    base.go
    ssh.go
    node.go
    ai.go
    user.go
    ssh_keygen.go
    checksum.go

  logging/
    logger.go

  settings/
    settings.go

  types/
    types.go

scripts/
  install.sh

.github/
  workflows/
    checks.yml
    release.yml
```

模块接口保持：

```go
type Module interface {
    ID() string
    Name() string
    Description() string
    DefaultEnabled() bool
    RequiresRoot() bool
    Dependencies() []string
    Check(ctx context.Context, sys *system.Context) CheckResult
    Plan(ctx context.Context, sys *system.Context, cfg *Config) ([]Step, error)
    Run(ctx context.Context, sys *system.Context, cfg *Config, log Logger) error
}
```

## 10. README 对齐要求

README 应与当前 CLI 行为一致，至少包括：

- `sys-bootstrap` 简介
- one-liner 安装 / 运行
- `user` / `full` run mode 概念
- 命令列表
- 支持系统
- 模块列表
- `plan` / `doctor` 说明
- `config` / `uninstall` 说明
- Legacy OneLineSetup 历史说明

旧 Bash 用法只放在 Legacy 小节，不作为主推荐路径。

## 11. 下一阶段强化方向

优先级建议：

1. 强化 `plan` / `doctor` 的信息完整性和可读性
2. 统一模块失败摘要格式，尽量稳定输出 stderr / exit code
3. 打磨 `user` 模块密码和补充更新流程
4. 完善 `ssh_keygen` 对已有密钥和覆盖确认的细节
5. 细化 `base` 模块的包级检查和日志表现

说明：

- 产品定位继续保持为个人/单机远端 vibecoding bootstrap 工具。
- 不为大批量部署设计独立配置文件驱动或声明式 apply 工作流。
- 用户级 AI 环境仍以交互式 `run` 为主，让用户自己确认和选择。
- docker / timezone / fail2ban 目前不进入近期实现计划，只保留为远期可选方向，不做接口预留或占位设计。

## 12. 当前验收标准

- `go test ./...` 在 CI 中通过
- `sys-bootstrap doctor` 可运行
- `sys-bootstrap plan` 和 `sys-bootstrap plan --json` 可运行
- `sys-bootstrap` 无参数时可执行 doctor 并进入主菜单
- `sys-bootstrap run` 可进入 Huh 交互并选择 run mode
- `sys-bootstrap module base` 可执行或在非 Linux 环境给出明确诊断
- installer 可下载并启动当前 release 二进制
- README 与实际 CLI 行为一致
