# sys-bootstrap 迁移执行计划

## 1. 产品目标

`sys-bootstrap` 是个人 Linux VM 开荒工具，用于替代当前 OneLineSetup Bash MVP。

最终目标：

- 使用 Go 编译为单文件二进制。
- 使用 `charmbracelet/huh` 提供交互式 CLI。
- 支持 one-liner 启动。
- 支持 `plan`、`doctor`、`run`、单模块执行和版本输出。
- 支持 Debian / Ubuntu 等 apt 系 Linux 发行版。
- Phase 1 直接完整迁移到 Go CLI，不再保留旧 `setup.sh` 入口。

项目命名统一迁移为 `sys-bootstrap`：

- README 主名改为 `sys-bootstrap`。
- Release artifact 使用 `sys-bootstrap_*`。
- installer 文案使用 `sys-bootstrap`。
- 旧 OneLineSetup 仅作为历史背景简要提及。

## 2. Phase 1 范围

Phase 1 覆盖当前仓库已有能力：

- `base`
- `ssh`
- `node`
- `ai`
- `user`
- `ssh_keygen`

当前 `gum` 模块废弃，不再作为 Go 版本模块保留，因为交互层由 Huh 接管。

不在 Phase 1 实现：

- docker
- timezone
- fail2ban
- Bubble Tea 复杂 TUI
- 持久 profile/config 文件

允许参考现有 Bash 模块逻辑，并可以重写、修复幂等性和安全问题。最终不需要保留 Bash MVP 的可执行兼容入口。

## 3. CLI 行为

二进制名：`sys-bootstrap`

必须支持命令：

```bash
sys-bootstrap
sys-bootstrap run
sys-bootstrap plan
sys-bootstrap doctor
sys-bootstrap module <id>
sys-bootstrap version
```

行为定义：

- `sys-bootstrap` 无参数时，先执行 `doctor` 检查并展示结果，然后询问是否进入 `run`。
- `sys-bootstrap run` 进入 Huh 交互执行流程。
- `sys-bootstrap plan` 只输出计划，不修改系统。
- `sys-bootstrap doctor` 检查环境，不修改系统。
- `sys-bootstrap module <id>` 执行单个模块。
- `sys-bootstrap version` 输出版本、commit、build date。

`run` 行为：

- `base` 永远最先执行，不允许用户取消。
- 用户可选择是否执行 `ssh`、`node`、`ai`、`user`、`ssh_keygen`。
- 如果选择 `ai`，必须先执行 `node`。
- `ai` 默认选择 Claude Code 和 Codex。

`module <id>` 行为：

- 如果模块有依赖缺失，提示用户确认是否补跑依赖。
- 用户拒绝补跑依赖时，中止并说明原因。
- 不要静默自动补依赖。

Phase 1 的 non-interactive 支持范围：

- `doctor`
- `plan`
- 无需额外输入的模块
- 需要输入的模块暂不要求完整 `--yes` 自动执行

## 4. 模块行为

### base

目标：

- apt update / upgrade。
- 安装基础包。
- 安装 zellij。

基础包来自现有 Bash MVP：

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

要求：

- 对已安装包跳过。
- 对已安装 zellij 跳过。
- 输出清晰步骤和失败摘要。

### ssh

默认端口继续使用 `22122`。

Phase 1 增强为可选 hardening：

- 修改 SSH 端口。
- 检查/写入 authorized_keys。
- 可选禁用 root login。
- 可选禁用 password login。

要求：

- 修改 `/etc/ssh/sshd_config` 前必须备份。
- 修改后必须运行 `sshd -t`。
- 校验失败必须回滚。
- 重启服务时兼容 `ssh` 和 `sshd` service 名称。
- 若检测到 active ufw，可询问是否放行新端口。
- 完成后提示用户测试新端口连接。

### node

目标：

- 安装 nvm。
- 安装 Node.js LTS。
- 安装 pnpm。
- 安装 bun。

要求：

- 已安装则跳过。
- 安装失败时说明具体工具和命令摘要。

### ai

默认选择：

- Claude Code
- Codex

行为：

- 依赖 `node`。
- 优先使用 pnpm，全局安装失败时再说明失败原因，不静默 fallback。
- Claude Code 包名：`@anthropic-ai/claude-code`
- Codex 包名：`@openai/codex`

### user

行为：

- Huh 收集用户名。
- Huh 收集默认 shell：`bash` 或 `zsh`。
- Huh 收集是否加入 sudo 组。
- 公钥输入支持：
  - 手动粘贴。
  - GitHub username 拉取 public keys。

密码处理：

- 不自动设置密码。
- 创建完成后提示用户手动运行 `passwd <user>`。

要求：

- 用户已存在时跳过创建，但可以提示是否补充 sudo 组和 authorized_keys。
- 公钥格式需要校验。
- `.ssh` 和 `authorized_keys` 权限必须正确。

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
3. 若存在严重不兼容项，禁止继续 run。
4. 若只有 warning，询问是否继续。
5. 进入 run。

`sys-bootstrap run`：

1. 显示模块选择。
2. 强制包含 `base`。
3. 若选择 `ai`，自动加入/提示加入 `node`。
4. 收集各模块所需输入。
5. 展示执行计划。
6. 用户确认后执行。

UI 原则：

- 不使用 Gum。
- 不引入 Bubble Tea。
- 不做复杂实时 TUI。
- 保持表单短而直接。

## 6. Plan / Doctor / 日志

### plan

`plan` Phase 1 必须同时支持文本和 JSON：

```bash
sys-bootstrap plan
sys-bootstrap plan --json
```

输出内容：

- 模块顺序。
- 每个模块将执行的高层步骤。
- 依赖关系。
- 当前环境检查结果。
- 已满足/待执行状态。
- 风险提示。

`plan` 不需要精确列出每一条 shell 命令，也不模拟全部安装逻辑。

### doctor

检查项：

- OS ID / version。
- 是否 apt 系发行版。
- CPU arch。
- root 状态。
- systemd。
- apt-get。
- bash。
- curl。
- network 基础可用性。
- sshd / ssh service 可用性。

exit code：

- 严重不兼容时非零。
- warning 仍返回 0。

### 日志

终端输出策略：

- 默认安静。
- 显示模块开始、完成、跳过、失败。
- 失败时展开 stdout/stderr 摘要和 exit code。

日志文件：

- 同时写入 `~/.local/state/sys-bootstrap/logs`。
- root 模式也写入当前用户可访问的 state 目录，不写 `/var/log`。

## 7. 权限与安装器

### 权限

不要程序启动即强制 root。

规则：

- `doctor` 和 `plan` 可普通用户运行。
- `run` / `module` 在执行需要 root 的模块前检查权限。
- 非 root 时提示用户用 sudo 重新运行。
- 不做自动 sudo re-exec。

### installer

one-liner 默认交互询问安装方式：

- 临时运行。
- sudo 安装到 `/usr/local/bin`。

sudo 安装触发方式：

- installer 交互询问。

installer 可以安装最小依赖：

- apt 系发行版可自动安装 `curl`、`ca-certificates` 等下载二进制所需依赖。
- installer 不承载业务逻辑。

installer 只做：

- 检查 OS / arch。
- 必要时安装最小下载依赖。
- 从 GitHub Release 下载匹配二进制。
- chmod。
- 临时运行或安装到 `/usr/local/bin`。

## 8. 发布与 CI

GitHub Release artifact 命名：

```text
sys-bootstrap_linux_amd64
sys-bootstrap_linux_arm64
```

Release workflow：

- tag push（`v*`）触发构建和发布。
- `CGO_ENABLED=0`。
- 至少构建：
  - linux-amd64
  - linux-arm64

Checks workflow：

- 需要。
- 至少运行：

```bash
go test ./...
```

Shellcheck 不作为 Phase 1 必须项。

## 9. 建议 Go 结构

建议目录：

```text
cmd/
  sys-bootstrap/
    main.go

internal/
  app/
    app.go
    config.go
    plan.go
    runner.go

  cli/
    commands.go

  ui/
    forms.go

  system/
    context.go
    distro.go
    command.go
    package_manager.go
    service.go

  modules/
    module.go
    registry.go
    base.go
    ssh.go
    node.go
    ai.go
    user.go
    ssh_keygen.go

  logging/
    logger.go

scripts/
  install.sh

.github/
  workflows/
    checks.yml
    release.yml
```

模块接口建议：

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

`Step` 建议：

```go
type Step struct {
    Module string `json:"module"`
    Title  string `json:"title"`
    Detail string `json:"detail"`
    Status string `json:"status"`
    Risk   string `json:"risk,omitempty"`
}
```

## 10. README 更新

README 必须同步更新为 Go 版本用法。

内容包括：

- `sys-bootstrap` 简介。
- one-liner 安装/运行。
- 命令列表。
- 支持系统。
- 模块列表。
- `plan` / `doctor` 示例。
- Legacy OneLineSetup 历史说明。

旧 Bash 用法只放在 Legacy 小节，不作为主推荐路径。

## 11. Phase 2 路线图

第一批继续 Go 化/强化优先级：

1. doctor / system checks
2. user
3. ssh_keygen
4. base

docker / timezone / fail2ban 只在路线图提及，不在 Phase 1 设计接口细节或占位模块。

## 12. MiMo 执行边界

MiMo 任务目标：

- 直接生成 Go CLI 骨架。
- 迁移当前 Bash MVP 的功能到 Go。
- 完成 Huh 交互流程。
- 完成 `plan` / `doctor` / `run` / `module` / `version`。
- 完成 installer。
- 完成 GitHub Actions。
- 更新 README。

MiMo 不需要保留旧 `setup.sh` 可执行兼容入口。

验收标准：

- `go test ./...` 通过。
- `sys-bootstrap doctor` 可运行。
- `sys-bootstrap plan` 和 `sys-bootstrap plan --json` 可运行。
- `sys-bootstrap run` 可进入 Huh 交互。
- `sys-bootstrap module base` 可执行或在非 Linux 环境给出明确诊断。
- README 与实际 CLI 行为一致。
