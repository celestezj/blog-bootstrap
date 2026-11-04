package toolchain

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/execx"
)

// Kind names the tools the installer knows how to provision.
type Kind string

const (
	Git  Kind = "git"
	Node Kind = "node"
)

// InstallKind classifies a failed system package installation.
type InstallKind string

const (
	KindMissing   InstallKind = "missing"   // no usable package manager
	KindPrivilege InstallKind = "privilege" // needs elevation / interactive auth
	KindFailed    InstallKind = "failed"    // network or unknown failure
)

// SystemInstallError is returned by the system-first installer.
type SystemInstallError struct {
	Kind    InstallKind
	Manager string
	Package string
	Command string
	Output  string
	Err     error
}

func (e *SystemInstallError) Error() string {
	return fmt.Sprintf("%s install via %s failed (%s): %s", e.Package, e.Manager, e.Kind, strings.TrimSpace(e.Output))
}
func (e *SystemInstallError) Unwrap() error { return e.Err }

// Installer performs system-first installations with explicit elevation.
type Installer struct {
	Runner execx.Runner
	GOOS   string
	IsRoot func() bool
}

// NewInstaller builds an Installer for the current platform.
func NewInstaller(r execx.Runner) *Installer {
	root := runtime.GOOS != "windows" && os.Geteuid() == 0
	return &Installer{
		Runner: r,
		GOOS:   runtime.GOOS,
		IsRoot: func() bool { return root },
	}
}

// Manager returns the first available package manager for this OS.
func (i *Installer) Manager(ctx context.Context) (string, bool) {
	var candidates []string
	switch i.GOOS {
	case "windows":
		candidates = []string{"winget"}
	case "darwin":
		candidates = []string{"brew"}
	default:
		candidates = []string{"apt-get", "dnf", "pacman"}
	}
	for _, c := range candidates {
		if _, err := i.Runner.LookPath(c); err == nil {
			return c, true
		}
	}
	return "", false
}

// Install installs git or Node.js through the given package manager.
func (i *Installer) Install(ctx context.Context, manager string, kind Kind) error {
	name, args, human, err := i.command(manager, kind)
	if err != nil {
		return &SystemInstallError{Kind: KindMissing, Manager: manager, Package: string(kind), Err: err}
	}
	if i.GOOS != "windows" && !i.IsRoot() {
		sudoPath, lookupErr := i.Runner.LookPath("sudo")
		if lookupErr != nil {
			return &SystemInstallError{
				Kind: KindPrivilege, Manager: manager, Package: string(kind),
				Command: human, Err: lookupErr,
				Output: "需要管理员权限，且找不到 sudo",
			}
		}
		sudoArgs := append([]string{"-n", name}, args...)
		res := i.Runner.Run(ctx, "", nil, sudoPath, sudoArgs...)
		if res.Failed() {
			return &SystemInstallError{
				Kind: KindPrivilege, Manager: manager, Package: string(kind),
				Command: human, Output: res.Combined(), Err: res.Err,
			}
		}
		return nil
	}
	res := i.Runner.Run(ctx, "", nil, name, args...)
	if res.Failed() {
		kindOf := KindFailed
		if looksLikePrivilege(res.Combined()) {
			kindOf = KindPrivilege
		}
		return &SystemInstallError{
			Kind: kindOf, Manager: manager, Package: string(kind),
			Command: human, Output: res.Combined(), Err: res.Err,
		}
	}
	return nil
}

func (i *Installer) command(manager string, kind Kind) (name string, args []string, human string, err error) {
	switch manager {
	case "winget":
		id := "Git.Git"
		if kind == Node {
			id = "OpenJS.NodeJS.LTS"
		}
		args = []string{"install", "--id", id, "-e", "--source", "winget",
			"--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}
		return "winget", args, execx.FormatCommand("winget", args), nil
	case "brew":
		pkg := "git"
		if kind == Node {
			pkg = "node"
		}
		args = []string{"install", pkg}
		return "brew", args, execx.FormatCommand("brew", args), nil
	case "apt-get":
		pkgs := []string{"git"}
		if kind == Node {
			pkgs = []string{"nodejs", "npm"}
		}
		args = append([]string{"install", "-y"}, pkgs...)
		return "apt-get", args, "sudo " + execx.FormatCommand("apt-get", args), nil
	case "dnf":
		pkgs := []string{"git"}
		if kind == Node {
			pkgs = []string{"nodejs", "npm"}
		}
		args = append([]string{"install", "-y"}, pkgs...)
		return "dnf", args, "sudo " + execx.FormatCommand("dnf", args), nil
	case "pacman":
		pkgs := []string{"git"}
		if kind == Node {
			pkgs = []string{"nodejs", "npm"}
		}
		args = append([]string{"-S", "--noconfirm"}, pkgs...)
		return "pacman", args, "sudo " + execx.FormatCommand("pacman", args), nil
	default:
		return "", nil, "", fmt.Errorf("unsupported package manager %q", manager)
	}
}

func looksLikePrivilege(output string) bool {
	lower := strings.ToLower(output)
	needles := []string{
		"permission denied", "not permitted", "requires elevation", "elevated",
		"administrator", "access is denied", "拒绝访问", "需要管理员", "提权",
		"a password is required", "a terminal is required", "must be run as root",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}
