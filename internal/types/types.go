package types

import (
	"context"
	"errors"
)

// ErrSSHPendingConfirmation is returned by the SSH module when the prepare
// phase completed but finalization requires the operator to test the new
// login from another terminal and re-run (or explicitly confirm).
var ErrSSHPendingConfirmation = errors.New("SSH hardening prepared — pending operator confirmation: test the new login from another terminal, then confirm")

// AccessPath describes one verified login path that can serve as a
// replacement during SSH hardening.
type AccessPath struct {
	Username       string
	UID            int
	GID            int
	HomeDir        string
	KeyFingerprint string
	PreparedPort   int
}

// CheckpointFunc is called during SSH preparation to wait for the operator
// to test the new login from another terminal and confirm finalization.
// Returning (false, nil) leaves the dual-path prepared state in place;
// (false, err) signals a terminal failure.
type CheckpointFunc func(ctx context.Context, candidates []AccessPath) (confirmed bool, err error)

// Config holds user selections for a run.
type Config struct {
	// APT mirror override (not serialized to plan --json)
	AptMirror string `json:"-"` // "cernet" or ""

	// SSH
	SSHPort        int
	SSHAddKey      bool
	SSHPublicKey   string
	SSHDisableRoot bool
	SSHDisablePass bool
	SSHAllowUFW    bool

	// Node
	InstallNVM  bool
	InstallNode bool
	InstallPnpm bool
	InstallBun  bool

	// AI
	InstallClaudeCode bool
	InstallCodex      bool

	// User
	NewUsername          string
	UserShell            string
	UserAddSudo          bool
	UserPasswordlessSudo bool
	UserAddKey           bool
	UserPublicKey        string
	UserKeySource        string // "paste" or "github"
	UserGitHubUser       string

	// SSH Keygen
	KeygenType      string // "ed25519" or "rsa"
	KeygenComment   string
	KeygenOverwrite bool

	// Docker
	DockerUser string

	// Timezone
	Timezone string

	// Fail2ban
	Fail2banMaxRetry int
	Fail2banFindTime string
	Fail2banBanTime  string
	Fail2banBackend  string
	Fail2banIgnoreIP string
}

// Step describes a single action in a plan.
type Step struct {
	Module string `json:"module"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status string `json:"status"`
	Risk   string `json:"risk,omitempty"`
}
