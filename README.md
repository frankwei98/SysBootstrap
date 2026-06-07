# sys-bootstrap

**从一台全新的 Linux 虚拟机到 AI Vibecoding，一条命令搞定。**

A single Go binary that turns a fresh Linux VM into a ready-to-vibe AI coding environment — Node.js, Claude Code, Codex, and everything in between.

## Quick Start

```bash
# One-liner — download, verify, and go
curl -fsSL https://raw.githubusercontent.com/frankwei98/SysBootstrap/main/scripts/install.sh | bash
```

中国用户可以使用：（jsdelivr有缓存，可能不是最新的脚本）

```bash
curl -fsSL https://cdn.jsdelivr.net/gh/frankwei98/SysBootstrap@main/scripts/install.sh | bash
```

Run the installer, pick **User-level tools**, select the modules you want, and you're coding with AI in minutes.

## What It Sets Up

| Module         | What You Get                                                                                             | Root |
| -------------- | -------------------------------------------------------------------------------------------------------- | ---- |
| **base**       | `git`, `curl`, `zsh`, `neovim`, `zellij`, system update                                                  | Yes  |
| **node**       | nvm → Node.js LTS → pnpm + bun                                                                           | No   |
| **ai**         | [Claude Code](https://docs.anthropic.com/en/docs/claude-code) + [Codex](https://github.com/openai/codex) | No   |
| **ssh**        | SSH hardening, custom port, key auth                                                                     | Yes  |
| **user**       | add user with/without sudo, import authorized SSH pubkey from GitHub / paste your pubkey                 | Yes  |
| **ssh_keygen** | ed25519 / RSA keypair generation for current user                                                        | No   |

**Typical vibecoding setup:** `node` + `ai` (no root needed) — done in under 5 minutes.

### The Fast Path: User-Level Install

If you just want AI coding tools on a VM where you already have a user account:

```bash
sys-bootstrap run
# Select: Node.js Environment + AI CLI Tools
# That's it.
```

This installs nvm, Node.js LTS, pnpm, bun, Claude Code, and Codex — all in your home directory, no root required.

### Full VM Provisioning

If you're setting up a fresh VM from scratch:

```bash
sys-bootstrap run
# Run mode: Full initialization
# Select everything you need
```

This handles system packages, SSH hardening, user creation, and the full Node.js + AI toolchain in one pass. The tool resolves module dependencies automatically (`ai` pulls in `node`, `base` is always first in full mode).

## Commands

```bash
sys-bootstrap              # Doctor check → interactive menu
sys-bootstrap run          # Pick modules, configure, execute
sys-bootstrap plan         # Preview what would happen
sys-bootstrap plan --json  # Machine-readable plan
sys-bootstrap doctor       # Check if your system is compatible
sys-bootstrap module <id>  # Run a single module (e.g. `sys-bootstrap module ai`)
sys-bootstrap uninstall    # Clean up user-level tools
sys-bootstrap config       # Language / APT mirror settings
sys-bootstrap version      # Version info
```

## Supported Systems

- **Debian 11+** / **Ubuntu 22+** (primary, tested)
- Linux Mint, Pop!\_OS, and other apt-based Debian/Ubuntu derivatives (compatible, untested)
- Architecture: `linux/amd64`, `linux/arm64`

## Why This Exists

Spinning up a new VM for AI-assisted coding should take minutes, not an afternoon of Googling "how to install nvm", "Node.js version manager", "pnpm global bin not found", and "Claude Code installation guide".

sys-bootstrap automates the whole chain:

1. **System prep** — base packages, shell setup, SSH hardening
2. **Node.js stack** — nvm → Node LTS → pnpm + bun, with shell PATH configured correctly
3. **AI tools** — Claude Code and Codex via pnpm global install, verified and ready

Every step is **idempotent** — run it again safely if something fails midway. The tool checks what's already installed and skips it.

## How It Works

```
You run the installer
  → Downloads the binary (SHA256 verified)
  → You pick user-level or full mode
  → You select modules via interactive TUI
  → sys-bootstrap generates an execution plan
  → You confirm → it runs
  → Reload shell → start vibecoding
```

The interactive UI is powered by [charmbracelet/huh](https://github.com/charmbracelet/huh). The binary is a single static Go executable with no runtime dependencies (other than `apt-get` for system-level modules).

## Safety

- **SSH changes**: backup → modify → validate with `sshd -t` → rollback on failure
- **Checksums**: nvm script and bun binaries verified against pinned SHA256 hashes
- **Idempotent**: every module checks current state before acting
- **Uninstall**: clean removal of user-level tools with shell rc file cleanup

## Building from Source

```bash
# Build
go build -o sys-bootstrap ./cmd/sys-bootstrap/

# Test
go test ./...

# Build with version injection
go build -ldflags="-s -w \
  -X github.com/frankwei98/sys-bootstrap/internal/app.Version=dev \
  -X github.com/frankwei98/sys-bootstrap/internal/app.Commit=$(git rev-parse --short HEAD) \
  -X github.com/frankwei98/sys-bootstrap/internal/app.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o sys-bootstrap ./cmd/sys-bootstrap/
```

## Legacy

This project was originally called **OneLineSetup** and used Bash + [Charmbracelet Gum](https://github.com/charmbracelet/gum). The Go rewrite compiles to a single static binary with no runtime dependencies. The original Bash version is preserved in git history.

## License

[MIT](LICENSE)
