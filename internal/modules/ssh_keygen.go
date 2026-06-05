package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
)

type SSHKeygenModule struct{}

func NewSSHKeygenModule() *SSHKeygenModule { return &SSHKeygenModule{} }

func (m *SSHKeygenModule) ID() string             { return "ssh_keygen" }
func (m *SSHKeygenModule) Name() string           { return "SSH Key Generation" }
func (m *SSHKeygenModule) Description() string    { return "Generate SSH keypair" }
func (m *SSHKeygenModule) DefaultEnabled() bool   { return false }
func (m *SSHKeygenModule) RequiresRoot() bool     { return false }
func (m *SSHKeygenModule) Dependencies() []string { return nil }

func (m *SSHKeygenModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	home, _ := os.UserHomeDir()
	keyFile := filepath.Join(home, ".ssh", "id_ed25519")
	if _, err := os.Stat(keyFile); err == nil {
		return CheckResult{Satisfied: true, Message: "ed25519 key already exists"}
	}
	return CheckResult{Satisfied: false, Message: "No SSH key found"}
}

func (m *SSHKeygenModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	keyType := cfg.KeygenType
	if keyType == "" {
		keyType = "ed25519"
	}
	return []types.Step{
		{Module: "ssh_keygen", Title: "Generate SSH keypair", Detail: fmt.Sprintf("Type: %s", keyType)},
	}, nil
}

func (m *SSHKeygenModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	keyType := cfg.KeygenType
	if keyType == "" {
		keyType = "ed25519"
	}

	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "id_"+keyType)

	os.MkdirAll(sshDir, 0o700)

	// Check if key already exists
	if _, err := os.Stat(keyFile); err == nil {
		if !cfg.KeygenOverwrite {
			log.Warnf("Key already exists: %s", keyFile)
			log.Info("Skipping key generation (use overwrite option to replace)")
			return nil
		}
		log.Warnf("Overwriting existing key: %s", keyFile)
	}

	// Build comment
	comment := cfg.KeygenComment
	if comment == "" {
		hostname, _ := os.Hostname()
		comment = sys.CurrentUser.Username + "@" + hostname
	}

	// Generate key
	log.Warn("Key will be created without passphrase (suitable for automation)")
	var args []string
	if keyType == "ed25519" {
		args = []string{"-t", "ed25519", "-C", comment, "-f", keyFile, "-N", ""}
	} else {
		args = []string{"-t", "rsa", "-b", "4096", "-C", comment, "-f", keyFile, "-N", ""}
	}

	// When overwriting, pipe "y" to confirm
	var res *system.Result
	var err error
	if cfg.KeygenOverwrite {
		res, err = system.RunWithInput("y\n", "ssh-keygen", args...)
	} else {
		res, err = system.Run("ssh-keygen", args...)
	}
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("ssh-keygen failed: %s", res.Stderr)
	}

	os.Chmod(keyFile, 0o600)
	os.Chmod(keyFile+".pub", 0o644)

	log.Success("SSH key generated:")
	log.Infof("  Private: %s", keyFile)
	log.Infof("  Public:  %s.pub", keyFile)

	pubContent, err := os.ReadFile(keyFile + ".pub")
	if err == nil {
		log.Info("Public key content:")
		fmt.Println(string(pubContent))
	}

	return nil
}
