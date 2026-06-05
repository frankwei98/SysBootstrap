package system

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
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
	HasSystemd     bool
	HasApt         bool
	HasBash        bool
	HasCurl        bool
	HasSSHD        bool
	HasUFW         bool
	UFWActive      bool
	HasNetwork     bool
	HasSSHDService bool
}

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
		ctx.UFWActive = isUFWActive()
	}

	// Network check — DNS resolution
	ctx.HasNetwork = checkNetwork()

	// SSH service availability (systemd unit, not just command)
	if ctx.HasSystemd {
		ctx.HasSSHDService = serviceExists("sshd") || serviceExists("ssh")
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
			fmt.Sscanf(major, "%d", &ctx.OSVersionMajor)
		} else {
			fmt.Sscanf(ctx.OSVersion, "%d", &ctx.OSVersionMajor)
		}
	}

	return nil
}

// IsSupportedOS returns true if the OS is a known apt-based distro
// (Debian 11+, Ubuntu 22+) or has apt-get available (other apt derivatives).
func (ctx *Context) IsSupportedOS() bool {
	switch ctx.OSID {
	case "debian":
		return ctx.OSVersionMajor >= 11
	case "ubuntu":
		return ctx.OSVersionMajor >= 22
	default:
		// Accept any distro that has apt-get (Linux Mint, Pop!_OS, etc.)
		return ctx.HasApt
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func isUFWActive() bool {
	out, err := exec.Command("ufw", "status").Output()
	if err != nil {
		return false
	}
	return parseUFWActive(string(out))
}

func parseUFWActive(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "Status: active" {
			return true
		}
	}
	return false
}

// checkNetwork tests basic DNS resolution as a proxy for network connectivity.
func checkNetwork() bool {
	addrs, err := net.LookupHost("deb.debian.org")
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
