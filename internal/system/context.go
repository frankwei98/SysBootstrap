package system

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Context holds runtime information about the current system.
type Context struct {
	OSID           string
	OSVersion      string
	OSVersionMajor int
	OSCodename     string
	Arch           string
	IsRoot         bool
	CurrentUser    *user.User
	InvokingUser   *user.User
	HasSystemd     bool
	HasApt         bool
	HasBash        bool
	HasCurl        bool
	HasSSHD        bool
	HasUFW         bool
	UFWActive      bool
	UFWStatusKnown bool
	HasNetwork     bool
	HasSSHDService bool
}

type SupportTier string

const (
	SupportTierPrimary       SupportTier = "primary"
	SupportTierAptCompatible SupportTier = "apt-compatible"
	SupportTierUnsupported   SupportTier = "unsupported"
)

// IsSupportedArchitecture reports the architectures covered by the release
// artifacts and the documented support matrix.
func IsSupportedArchitecture(arch string) bool {
	switch arch {
	case "linux/amd64", "linux/arm64":
		return true
	default:
		return false
	}
}

var sbinSearchPaths = []string{"/usr/local/sbin", "/usr/sbin", "/sbin"}

// NewContext creates a system context by detecting the current environment.
func NewContext() (*Context, error) {
	ctx := &Context{
		Arch: runtime.GOOS + "/" + runtime.GOARCH,
	}

	// Current user
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("cannot determine current user: %w", err)
	}
	ctx.CurrentUser = u
	ctx.IsRoot = u.Uid == "0"
	if ctx.IsRoot {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
			if invokingUser, lookupErr := user.Lookup(sudoUser); lookupErr == nil {
				ctx.InvokingUser = invokingUser
			}
		}
	}

	// OS detection
	if err := ctx.detectOS(); err != nil {
		return nil, err
	}

	// Tool detection
	ctx.HasSystemd = commandExists("systemctl")
	ctx.HasApt = commandExists("apt-get")
	ctx.HasBash = commandExists("bash")
	ctx.HasCurl = commandExists("curl")
	ctx.HasSSHD = commandExists("sshd")
	ctx.HasUFW = commandExists("ufw")

	if ctx.HasUFW {
		ctx.UFWActive, ctx.UFWStatusKnown = detectUFWStatus()
	}

	// Network check — DNS resolution
	ctx.HasNetwork = checkNetwork()

	// SSH availability can be supplied by a traditional service or by a
	// socket-activated unit on current Ubuntu releases.
	if ctx.HasSystemd {
		ctx.HasSSHDService = serviceExists("sshd") || serviceExists("ssh") || socketExists("sshd") || socketExists("ssh")
	}

	return ctx, nil
}

// detectOS reads /etc/os-release and populates OS fields.
func (ctx *Context) detectOS() error {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return fmt.Errorf("cannot detect OS: /etc/os-release not found")
	}
	defer f.Close()

	fields := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "="); idx > 0 {
			key := line[:idx]
			val := strings.Trim(line[idx+1:], "\"")
			fields[key] = val
		}
	}

	ctx.OSID = fields["ID"]
	ctx.OSVersion = fields["VERSION_ID"]
	ctx.OSCodename = fields["VERSION_CODENAME"]

	if ctx.OSVersion == "" {
		ctx.OSVersion = "unknown"
	}

	// Extract major version (22.04 -> 22, 12 -> 12)
	if ctx.OSVersion != "unknown" {
		if dot := strings.Index(ctx.OSVersion, "."); dot > 0 {
			major := ctx.OSVersion[:dot]
			ctx.OSVersionMajor, _ = strconv.Atoi(major)
		} else {
			ctx.OSVersionMajor, _ = strconv.Atoi(ctx.OSVersion)
		}
	}

	return nil
}

// IsSupportedOS returns true if the OS is a known apt-based distro
// (Debian 11+, Ubuntu 22+) or has apt-get available (other apt derivatives).
func (ctx *Context) IsSupportedOS() bool {
	if ctx.IsPrimarySupportedOS() {
		return true
	}
	return ctx.HasApt
}

// IsPrimarySupportedOS returns true only for distros that are explicitly
// treated as the primary supported targets.
func (ctx *Context) IsPrimarySupportedOS() bool {
	switch ctx.OSID {
	case "debian":
		return ctx.OSVersionMajor >= 11
	case "ubuntu":
		return ctx.OSVersionMajor >= 22
	default:
		return false
	}
}

// SupportTier reports whether the current system is a primary supported
// target, only apt-compatible, or unsupported.
func (ctx *Context) SupportTier() SupportTier {
	switch {
	case ctx.IsPrimarySupportedOS():
		return SupportTierPrimary
	case ctx.HasApt:
		return SupportTierAptCompatible
	default:
		return SupportTierUnsupported
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	if err == nil {
		return true
	}

	for _, dir := range sbinSearchPaths {
		path := filepath.Join(dir, name)
		info, statErr := os.Stat(path)
		if statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}

	return false
}

var ufwStatusOutputFn = func() ([]byte, error) {
	return exec.Command("ufw", "status").Output()
}

func detectUFWStatus() (active, known bool) {
	out, err := ufwStatusOutputFn()
	if err != nil {
		return false, false
	}
	return parseUFWStatus(string(out))
}

func parseUFWActive(out string) bool {
	active, _ := parseUFWStatus(out)
	return active
}

func parseUFWStatus(out string) (active, known bool) {
	for _, line := range strings.Split(out, "\n") {
		switch strings.TrimSpace(line) {
		case "Status: active":
			return true, true
		case "Status: inactive":
			return false, true
		}
	}
	return false, false
}

// checkNetwork tests basic DNS resolution as a proxy for network connectivity.
func checkNetwork() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, "deb.debian.org")
	return err == nil && len(addrs) > 0
}

// serviceExists checks if a systemd unit file exists for the given service.
func serviceExists(name string) bool {
	out, err := exec.Command("systemctl", "list-unit-files", name+".service").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), name+".service")
}

func socketExists(name string) bool {
	out, err := exec.Command("systemctl", "list-unit-files", name+".socket").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), name+".socket")
}
