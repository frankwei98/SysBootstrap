package types

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
}

// Step describes a single action in a plan.
type Step struct {
	Module string `json:"module"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status string `json:"status"`
	Risk   string `json:"risk,omitempty"`
}
