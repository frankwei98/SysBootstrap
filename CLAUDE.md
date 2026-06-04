# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OneLineSetup is a modular bash script for server provisioning on Debian 11+ and Ubuntu 22+. It uses [Charmbracelet Gum](https://github.com/charmbracelet/gum) for interactive terminal UI.

## Usage

```bash
# Interactive mode (gum menu)
sudo bash setup.sh

# Specific modules
sudo bash setup.sh --modules ssh,node,ai

# All modules
sudo bash setup.sh --all
```

Must run as root. Not designed for `curl | bash` (local execution only).

## Architecture

**Execution flow:** `setup.sh` → load `lib/` → load `modules/` → run modules in order

- **lib/common.sh** — Logging (`log_info`, `log_success`, `log_warn`, `log_error`, `die`), `require_root`, `ensure_installed` (idempotent apt install)
- **lib/detect.sh** — OS detection and version validation. Populates `OS_ID`, `OS_VERSION`, `OS_VERSION_MAJOR`, `OS_CODENAME`
- **modules/*.sh** — Each module exports a `module_xxx()` function. Auto-discovered by glob in `setup.sh`

## Module Conventions

- **Function name** must match filename: `ssh.sh` → `module_ssh()`
- **Idempotency** is required — check if already installed/configured before acting (use `command -v`, `dpkg -s`, file existence checks)
- **Always use `set -euo pipefail`** at the top of every file
- **Error handling** — use `|| die "message"` after curl/install commands
- **User interaction** — use `gum input`, `gum choose`, `gum confirm`, `gum write` (gum is always available after `module_base` + `module_gum` run)
- **New modules** need to be registered in the `AVAILABLE_MODULES` array in `setup.sh`

## Module Dependency Order

```
base → gum → ssh → node → ai
                    ↘ user
                    ↘ ssh_keygen
```

`base` and `gum` always run first. `node` must run before `ai`. Other modules are independent.

## Key Safety Patterns

- SSH config changes: backup → modify → `sshd -t` validate → restore on failure → restart
- Port validation: numeric check, range 1-65535, reject port 22
- Public key validation: regex check for `ssh-rsa`, `ssh-ed25519`, `ecdsa-sha2`, `sk-` prefixes
- Always warn user to test new SSH port before closing old connection
