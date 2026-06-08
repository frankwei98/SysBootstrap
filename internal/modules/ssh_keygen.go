package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
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
	home := system.TargetHomeDir(sys)
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
	keyFile := filepath.Join(system.TargetHomeDir(sys), ".ssh", "id_"+keyType)
	if _, err := os.Stat(keyFile); err == nil && !cfg.KeygenOverwrite {
		return nil, nil
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

	home := system.TargetHomeDir(sys)
	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "id_"+keyType)

	if sys != nil && sys.InvokingUser != nil {
		if res, err := system.RunAsUserWithInput(sys, "", "mkdir", "-p", sshDir); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to create .ssh directory", res, err)
		}
		if res, err := system.RunAsUserWithInput(sys, "", "chmod", "700", sshDir); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to set .ssh permissions", res, err)
		}
	} else {
		os.MkdirAll(sshDir, 0o700)
	}

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
		comment = system.TargetUsername(sys) + "@" + hostname
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
		res, err = system.RunAsUserWithInput(sys, "y\n", "ssh-keygen", args...)
	} else {
		res, err = system.RunAsUserWithInput(sys, "", "ssh-keygen", args...)
	}
	if err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("ssh-keygen failed", res, err)
	}

	if sys != nil && sys.InvokingUser != nil {
		system.RunAsUserWithInput(sys, "", "chmod", "600", keyFile)
		system.RunAsUserWithInput(sys, "", "chmod", "644", keyFile+".pub")
	} else {
		os.Chmod(keyFile, 0o600)
		os.Chmod(keyFile+".pub", 0o644)
	}

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
