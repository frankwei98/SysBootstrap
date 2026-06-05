# OneLineSetup

一键开荒 Debian / Ubuntu 服务器 — SSH 加固、用户创建、Node.js 环境、AI CLI 工具，全部通过交互式菜单完成。

## 支持系统

- Debian 11+
- Ubuntu 22+

## 快速开始

**一行命令 (curl | bash):**

```bash
curl -fsSL https://cdn.jsdelivr.net/gh/frankwei98/OneLineConfig@main/setup.sh | sudo bash
```

**或克隆到本地:**

```bash
git clone https://github.com/frankwei98/OneLineConfig.git
cd OneLineConfig

# 交互式模式 (推荐)
sudo bash setup.sh

# 一键安装全部模块
sudo bash setup.sh --all

# 指定模块
sudo bash setup.sh --modules ssh,node,ai
```

> 必须以 root 身份运行 (`sudo`)。

## 模块列表

| 模块 | 说明 |
|------|------|
| `base` | 系统更新 + 基础工具 (git, curl, neovim, zellij 等) |
| `gum` | 安装 [Charmbracelet Gum](https://github.com/charmbracelet/gum) 交互式 UI |
| `ssh` | 修改 SSH 端口、校验配置、检查/添加公钥 |
| `node` | nvm + Node.js LTS + pnpm + bun |
| `ai` | 安装 Claude Code / Codex CLI |
| `user` | 创建用户、加入 sudo 组、写入 SSH 公钥 |
| `ssh_keygen` | 生成 ed25519 / RSA 密钥对 |

## 模块依赖

```
base → gum → ssh → node → ai
                    ↘ user
                    ↘ ssh_keygen
```

`base` 和 `gum` 始终最先执行，`node` 必须在 `ai` 之前。其余模块互相独立。

## 安全设计

- SSH 配置修改前自动备份，`sshd -t` 校验失败自动回滚
- 端口校验：数字范围 1-65535，拒绝 22
- 公钥格式校验：`ssh-rsa` / `ssh-ed25519` / `ecdsa-sha2` / `sk-`
- 所有模块幂等 — 重复执行安全无副作用

## 自行添加模块

1. 在 `modules/` 下新建 `xxx.sh`
2. 定义 `module_xxx()` 函数
3. 在 `setup.sh` 的 `AVAILABLE_MODULES` 数组中注册

```bash
# modules/xxx.sh
#!/usr/bin/env bash
set -euo pipefail

module_xxx() {
    log_info "=== 我的模块 ==="
    # ...
}
```

## License

[MIT](LICENSE)
