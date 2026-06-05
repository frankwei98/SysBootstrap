# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

sys-bootstrap is a personal Linux VM provisioning tool, written in Go. It compiles to a single binary and uses [charmbracelet/huh](https://github.com/charmbracelet/huh) for interactive terminal UI.

Supports Debian 11+ and Ubuntu 22+ (and any apt-based distro).

## Usage

```bash
# Default: doctor check → interactive provisioning
sys-bootstrap

# Interactive provisioning
sys-bootstrap run

# Show execution plan (text or JSON)
sys-bootstrap plan
sys-bootstrap plan --json

# Check system compatibility
sys-bootstrap doctor

# Run a single module
sys-bootstrap module <id>

# Version info
sys-bootstrap version
```

## Build & Test

```bash
# Build
go build -o sys-bootstrap ./cmd/sys-bootstrap/

# Test
go test ./...

# Build with version injection
go build -ldflags="-s -w \
  -X github.com/FrankWiZe/sys-bootstrap/internal/app.Version=dev \
  -X github.com/FrankWiZe/sys-bootstrap/internal/app.Commit=$(git rev-parse --short HEAD) \
  -X github.com/FrankWiZe/sys-bootstrap/internal/app.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o sys-bootstrap ./cmd/sys-bootstrap/
```

## Architecture

**Execution flow:** `cmd/sys-bootstrap/main.go` → `internal/cli/` → `internal/app/` → `internal/modules/`

```
cmd/sys-bootstrap/main.go   Entry point, subcommand routing
internal/
  cli/commands.go            CLI command implementations (RunCmd, PlanCmd, DoctorCmd, ModuleCmd, VersionCmd)
  app/
    app.go                   Registry setup, registers all modules
    plan.go                  Plan generation and formatting (text + JSON)
    runner.go                Module execution engine (dependency order, root check, logging)
    version.go               Build-time version variables
  modules/
    module.go                Module interface definition
    registry.go              Module registry with dependency resolution (topological sort)
    base.go                  apt update/upgrade, base packages, zellij
    ssh.go                   SSH hardening (port, keys, root/password login)
    node.go                  nvm, Node.js LTS, pnpm, bun
    ai.go                    Claude Code, Codex (pnpm global install)
    user.go                  System user creation with sudo/SSH key options
    ssh_keygen.go            SSH keypair generation (ed25519/rsa)
  system/
    context.go               OS detection, tool detection, root check
    command.go               Command execution helpers (Run, RunWithContext, RunWithInput, DpkgInstalled, CommandExists)
  types/types.go             Config struct, Step struct (plan output)
  ui/forms.go                Huh interactive forms
  logging/logger.go          Colorized terminal + file logging (~/.local/state/sys-bootstrap/logs/)
```

## Module Interface

All modules implement `modules.Module`:

```go
type Module interface {
    ID() string
    Name() string
    Description() string
    DefaultEnabled() bool
    RequiresRoot() bool
    Dependencies() []string
    Check(ctx context.Context, sys *system.Context) CheckResult
    Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error)
    Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error
}
```

## Module Conventions

- **Idempotency** is required — `Check()` should return `Satisfied: true` when already configured
- **Dependencies** — declare in `Dependencies()`, registry resolves topological order
- **Root check** — set `RequiresRoot() bool`; runner enforces before execution
- **Error handling** — return descriptive errors with context; runner logs stderr on failure
- **User interaction** — use `huh` forms in `ui/forms.go`, not inline in modules
- **New modules** — register in `app.NewRegistry()` in `internal/app/app.go`

## Module Dependency Order

```
base (always first, mandatory)
  ↓
node → ai (ai depends on node)
ssh, user, ssh_keygen (independent)
```

## Key Safety Patterns

- SSH config changes: backup → modify → `sshd -t` validate → rollback on failure → restart
- Port validation: numeric check, range 1-65535
- Public key validation: regex for `ssh-rsa`, `ssh-ed25519`, `ecdsa-sha2`, `sk-` prefixes
- Always warn user to test new SSH port before closing old connection
- Dependency prompt: `module <id>` checks unsatisfied deps and asks user before auto-running them

## Release

Releases are tag-based (`v*`), built via GitHub Actions with `CGO_ENABLED=0` for linux/amd64 and linux/arm64. Install via `scripts/install.sh`.
