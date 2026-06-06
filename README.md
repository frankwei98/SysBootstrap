# sys-bootstrap

Personal Linux VM provisioning tool — a single Go binary that sets up your server from scratch.

## Quick Start

```bash
# Install or run interactively
curl -fsSL https://raw.githubusercontent.com/frankwei98/SysBootstrap/main/scripts/install.sh | bash
```

如果你在中国，运行：

```
curl -fsSL https://cdn.jsdelivr.net/gh/frankwei98/SysBootstrap@main/scripts/install.sh | bash
```

The installer detects root/sudo capability and automatically uses `sudo` when the selected action requires root, including temporary runs and installation to `/usr/local/bin`.

## Commands

```bash
sys-bootstrap              # Run doctor check, then interactive provisioning
sys-bootstrap run          # Interactive module selection and execution
sys-bootstrap plan         # Show execution plan (text)
sys-bootstrap plan --json  # Show execution plan (JSON)
sys-bootstrap doctor       # Check system compatibility
sys-bootstrap module <id>  # Run a single module
sys-bootstrap uninstall    # Uninstall user-level software (interactive)
sys-bootstrap uninstall --dry-run  # Show what would be removed, without changes
sys-bootstrap uninstall --all --yes  # Non-interactive uninstall of all detected items
sys-bootstrap version      # Show version info
```

## Supported Systems

**Primary (tested):**

- Debian 11+
- Ubuntu 22+

**Compatible (untested, apt-based):**

- Linux Mint, Pop!\_OS, and other Debian/Ubuntu derivatives with `apt-get`

## Modules

| Module       | Description                                | Root |
| ------------ | ------------------------------------------ | ---- |
| `base`       | System update, essential packages, zellij  | Yes  |
| `ssh`        | SSH port change, key management, hardening | Yes  |
| `node`       | nvm, Node.js LTS, pnpm, bun                | No   |
| `ai`         | Claude Code, Codex                         | No   |
| `user`       | Create system user with sudo/SSH options   | Yes  |
| `ssh_keygen` | Generate SSH keypair                       | No   |

## Examples

```bash
# Check your system
sys-bootstrap doctor

# See what would happen
sys-bootstrap plan

# Run everything interactively
sys-bootstrap run

# Run just the SSH module
sys-bootstrap module ssh
```

## Building from Source

```bash
go build -o sys-bootstrap ./cmd/sys-bootstrap/
```

## Legacy

This project was originally called **OneLineSetup** and used Bash with [Charmbracelet Gum](https://github.com/charmbracelet/gum) for interactive prompts. The Go rewrite uses [charmbracelet/huh](https://github.com/charmbracelet/huh) and provides a single static binary with no runtime dependencies.

The original Bash version is preserved in git history for reference.

## License

[MIT](LICENSE)
