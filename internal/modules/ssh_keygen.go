package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

var sshKeygenCommandExistsFn = system.CommandExists

type SSHKeygenModule struct{}

func NewSSHKeygenModule() *SSHKeygenModule { return &SSHKeygenModule{} }

func (m *SSHKeygenModule) ID() string             { return "ssh_keygen" }
func (m *SSHKeygenModule) Name() string           { return "SSH Key Generation" }
func (m *SSHKeygenModule) Description() string    { return "Generate SSH keypair" }
func (m *SSHKeygenModule) DefaultEnabled() bool   { return false }
func (m *SSHKeygenModule) RequiresRoot() bool     { return false }
func (m *SSHKeygenModule) Dependencies() []string { return nil }

func (m *SSHKeygenModule) Check(ctx context.Context, sys *system.Context, cfg *types.Config) CheckResult {
	home := system.TargetHomeDir(sys)
	keyType := "ed25519"
	overwrite := false
	if cfg != nil {
		if cfg.KeygenType != "" {
			keyType = cfg.KeygenType
		}
		overwrite = cfg.KeygenOverwrite
	}
	keyFile := filepath.Join(home, ".ssh", "id_"+keyType)
	if _, err := os.Stat(keyFile); err == nil {
		if overwrite {
			return CheckResult{Satisfied: false, Message: fmt.Sprintf("%s key exists and overwrite was requested", keyType)}
		}
		if _, err := os.Stat(keyFile + ".pub"); err != nil {
			return CheckResult{Satisfied: false, Message: fmt.Sprintf("%s public key is missing", keyType)}
		}
		return CheckResult{Satisfied: true, Message: fmt.Sprintf("%s key already exists", keyType)}
	}
	return CheckResult{Satisfied: false, Message: "No SSH key found"}
}

func (m *SSHKeygenModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	if cfg == nil {
		return nil, nil
	}
	keyType := cfg.KeygenType
	if keyType == "" {
		keyType = "ed25519"
	}
	keyFile := filepath.Join(system.TargetHomeDir(sys), ".ssh", "id_"+keyType)
	if _, err := os.Stat(keyFile); err == nil && !cfg.KeygenOverwrite {
		if _, err := os.Stat(keyFile + ".pub"); err == nil {
			return nil, nil
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect SSH public key: %w", err)
		}
		return []types.Step{
			{Module: "ssh_keygen", Title: "Recover SSH public key", Detail: keyFile + ".pub"},
		}, nil
	}
	return []types.Step{
		{Module: "ssh_keygen", Title: "Generate SSH keypair", Detail: fmt.Sprintf("Type: %s", keyType)},
	}, nil
}

func (m *SSHKeygenModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if !sshKeygenCommandExistsFn("ssh-keygen") {
		return fmt.Errorf("ssh-keygen is required; install openssh-client (for example: sudo apt-get install openssh-client)")
	}

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
			publicKeyFile := keyFile + ".pub"
			if _, err := os.Stat(publicKeyFile); err == nil {
				log.Warnf("Key already exists: %s", keyFile)
				log.Info("Skipping key generation (use overwrite option to replace)")
				return nil
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("inspect SSH public key: %w", err)
			}

			res, err := system.RunAsUserWithInputContext(ctx, sys, "", "ssh-keygen", "-y", "-f", keyFile)
			if err != nil || res == nil || res.ExitCode != 0 {
				return system.FormatCommandError("failed to recover SSH public key", res, err)
			}
			publicKey := strings.TrimSpace(res.Stdout)
			if publicKey == "" {
				return fmt.Errorf("failed to recover SSH public key: ssh-keygen returned empty output")
			}
			if err := system.WriteFileAtomicallyAsInvokingUser(publicKeyFile, []byte(publicKey+"\n"), 0o644); err != nil {
				return fmt.Errorf("write recovered SSH public key: %w", err)
			}
			log.Successf("SSH public key recovered: %s", publicKeyFile)
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
