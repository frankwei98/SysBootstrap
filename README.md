# sys-bootstrap

**从一台全新的 Linux VM 到远端 vibecoding 搭子，一条命令搞定。**

`sys-bootstrap` is a single Go binary for turning a fresh Debian/Ubuntu-style VM into a practical remote AI coding box: base packages, SSH hardening, Node.js, Claude Code, Codex, user creation, SSH key generation, Docker, timezone setup, and fail2ban in one interactive flow.

It is designed for real remote vibecoding work, not just package installation. In practice that usually means working from a normal sudo-capable user account instead of a root-only session. Some "full access" or autonomous coding workflows behave better that way, and `sys-bootstrap` can help you get there quickly.

## Quick Start

```bash
curl -fsSL https://19yo.de/systrap | bash
# or if you don't like shortlink then: 
curl -fsSL https://raw.githubusercontent.com/frankwei98/SysBootstrap/main/scripts/install.sh | bash
```

China mainland mirror:

```bash
curl -fsSL https://cdn.jsdelivr.net/gh/frankwei98/SysBootstrap@main/scripts/install.sh | bash
```

The installer will ask for:

- language
- download region
- run mode
- APT mirror preference
- temporary run or install to `/usr/local/bin`

## Two Paths

### User Mode

For an existing VM user who just wants AI tooling:

```bash
sys-bootstrap run
# Choose run mode: user
# Select: node, ai, ssh_keygen
```

This mode is aimed at the fast path: install Node.js, pnpm, bun, Claude Code, Codex, and optionally generate SSH keys, all without touching system packages.

### Full Mode

For setting up a fresh VM end to end:

```bash
sys-bootstrap run
# Choose run mode: full
# base is always included
# Select any extra modules you need
```

Full mode runs `base` first, then optionally `ssh`, `node`, `ai`, `user`, `ssh_keygen`, `docker`, `timezone`, and `fail2ban`. If you select `ai`, `node` is added automatically.

## What It Sets Up

| Module | What You Get | Root |
| --- | --- | --- |
| **base** | `git`, `curl`, `zsh`, `neovim`, `zellij`, apt update/upgrade | Yes |
| **node** | nvm, Node.js LTS, pnpm, bun | No |
| **ai** | [Claude Code](https://docs.anthropic.com/en/docs/claude-code) and [Codex](https://github.com/openai/codex) | No |
| **ssh** | SSH hardening, custom port, authorized keys, optional auth tightening | Yes |
| **user** | create a normal user, optional sudo, passwordless sudo, GitHub/pasted SSH keys | Yes |
| **ssh_keygen** | ed25519 / RSA keypair generation for the current target user | No |
| **docker** | Docker Engine, CLI, Compose plugin, docker group setup | Yes |
| **timezone** | system timezone management via `timedatectl` | Yes |
| **fail2ban** | fail2ban install plus default SSH jail protection | Yes |

## Why This Exists

A new VM should become a usable coding machine in minutes, not after an afternoon of copying install guides for nvm, Node.js, pnpm, bun, Claude Code, Codex, SSH, shell setup, and user permissions.

`sys-bootstrap` wraps that into one interactive CLI:

1. System prep
2. User/account shaping
3. Node.js tooling
4. AI CLI setup
5. Safe re-runs if you need to come back later

For remote vibecoding, a regular sudoer account is often the sweet spot. It is safer than living in `root`, but still practical for tools that need broad machine access. `sys-bootstrap` reflects that workflow directly.

It is intentionally a single-machine interactive bootstrap tool. It is not planned to grow into a full profile/config management system or a declarative `apply` workflow for large-scale fleet rollout.

## Commands

```bash
sys-bootstrap                      # doctor -> main menu (provisioning / settings / exit)
sys-bootstrap run                  # interactive provisioning flow
sys-bootstrap plan                 # preview module capabilities for this machine
sys-bootstrap plan --json          # machine-readable preview
sys-bootstrap doctor               # compatibility check
sys-bootstrap module <id>          # run a single module
sys-bootstrap uninstall            # remove user-level tools and shell wiring
sys-bootstrap config               # interactive language / APT mirror settings
sys-bootstrap config language en
sys-bootstrap config language zh-CN
sys-bootstrap config apt-mirror default
sys-bootstrap config apt-mirror cernet
sys-bootstrap version              # version, commit, build date, Go version, platform
```

## How It Works

```text
You run the installer
  -> It checks OS / arch and installs minimal download dependencies if needed
  -> It asks for language, region, run mode, and APT mirror
  -> It downloads the matching release binary
  -> It verifies SHA256 when available, or asks before continuing without it
  -> You choose temporary run or install to /usr/local/bin
  -> sys-bootstrap runs doctor
  -> You enter provisioning or settings
  -> sys-bootstrap builds a plan
  -> You confirm and it executes
```

The interactive UI is powered by [charmbracelet/huh](https://github.com/charmbracelet/huh). The binary is a single Go executable with no long-running service and no separate runtime package to manage.

## Plan, Doctor, Config

### `doctor`

Checks:

- OS and version
- whether the distro is supported or apt-compatible
- architecture
- root state
- systemd
- `apt-get`
- `bash`
- `curl`
- basic network resolution
- `sshd`
- `ssh` / `sshd` service availability

Fatal incompatibilities return a non-zero exit code. Warnings still return `0`.

Current output also includes a short conclusion line so you can tell at a glance whether the machine looks compatible enough to continue.

### `plan`

`plan` is a capability preview, not a byte-for-byte dry run of one exact future execution.

It combines:

- current machine state
- module checks
- saved language / APT mirror settings
- default module behavior

In practice that means:

- the SSH module preview still assumes the default target port `22122` unless you later override it in the interactive flow
- the SSH module now recognizes already-satisfied port/service state instead of always appearing as pending
- `fail2ban` follows the currently configured system SSH port when no explicit SSH target port has been chosen
- if SSH changes the port on a machine that already has a fail2ban `sshd` jail, sys-bootstrap syncs that jail to the new port
- the standalone interactive `timezone` module defaults to the detected current timezone, while `plan` still previews the product default target of `Etc/UTC`
- plan text includes an `Overview` line with pending / satisfied / awaiting-input counts
- the `user` module is treated as config-sensitive: existing users can preview as either already matching the requested state or needing only supplemental updates

Text output is for humans. JSON output is for tooling.

### `config`

The current persisted settings are intentionally small:

- CLI language
- APT mirror preference

That keeps the tool simple while still making repeat usage less annoying.

## Safety

- SSH changes follow backup -> edit -> `sshd -t` -> rollback on failure
- nvm installer and bun release assets are checksum-verified in the module flow
- modules are designed to be re-runnable
- logs are written to `~/.local/state/sys-bootstrap/logs`
- `uninstall` removes user-level tools and cleans shell rc entries

## Supported Systems

- **Debian 11+** and **Ubuntu 22+**: primary supported targets
- Linux Mint, Pop!_OS, and other apt-based Debian/Ubuntu derivatives: compatible path
- Architecture: `linux/amd64`, `linux/arm64`

## Validation

Recent real-machine validation notes:

- [Fresh ARM64 VM Validation (2026-06-08)](/home/frank/SysBootstrap/docs/validation/2026-06-08-arm64-fresh-vm.md)

## Building From Source

```bash
go build -o sys-bootstrap ./cmd/sys-bootstrap/
go test ./...

go build -ldflags="-s -w \
  -X github.com/frankwei98/sys-bootstrap/internal/app.Version=dev \
  -X github.com/frankwei98/sys-bootstrap/internal/app.Commit=$(git rev-parse --short HEAD) \
  -X github.com/frankwei98/sys-bootstrap/internal/app.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o sys-bootstrap ./cmd/sys-bootstrap/
```

## Legacy

This project started as **OneLineSetup**, a Bash-based setup script with Gum for interaction. The current Go version is the maintained product path. The old Bash approach remains part of the project's history, not the recommended entrypoint.

## License

[MIT](LICENSE)
