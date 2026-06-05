# sys-bootstrap

Personal Linux VM provisioning tool — a single Go binary that sets up your server from scratch.

## Quick Start

```bash
# Install and run interactively
curl -fsSL https://raw.githubusercontent.com/FrankWiZe/OneLineSetup/main/scripts/install.sh | bash

# Or install to /usr/local/bin
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/FrankWiZe/OneLineSetup/main/scripts/install.sh)"
```

## Commands

```bash
sys-bootstrap              # Run doctor check, then interactive provisioning
sys-bootstrap run          # Interactive module selection and execution
sys-bootstrap plan         # Show execution plan (text)
sys-bootstrap plan --json  # Show execution plan (JSON)
sys-bootstrap doctor       # Check system compatibility
sys-bootstrap module <id>  # Run a single module
sys-bootstrap version      # Show version info
```

## Supported Systems

- Debian 11+
- Ubuntu 22+

## Modules

| Module | Description | Root |
|--------|-------------|------|
| `base` | System update, essential packages, zellij | Yes |
| `ssh` | SSH port change, key management, hardening | Yes |
| `node` | nvm, Node.js LTS, pnpm, bun | No |
| `ai` | Claude Code, Codex | No |
| `user` | Create system user with sudo/SSH options | Yes |
| `ssh_keygen` | Generate SSH keypair | No |

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
