package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Pinned patch releases for major-only .node-version values.
var nodePins = map[int]string{
	18: "18.20.5",
	20: "20.18.1",
	22: "22.14.0",
	24: "24.0.0",
}

const DefaultNodeVersion = "22.14.0"
const DefaultGitVersion = "2.47.1"

// ResolveNodeDistVersion turns "22", "22.14" or "22.14.0" into a concrete
// official release version.
func ResolveNodeDistVersion(want string) (string, error) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if want == "" {
		return DefaultNodeVersion, nil
	}
	parts := strings.Split(want, ".")
	if len(parts) >= 3 {
		return want, nil
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid node version %q", want)
	}
	pinned, ok := nodePins[major]
	if !ok {
		return "", fmt.Errorf("unsupported Node.js major version %d", major)
	}
	return pinned, nil
}

// NodeSpec builds the official Node.js archive spec for the platform.
func NodeSpec(goos, goarch, want, toolsDir string) (PortableSpec, error) {
	version, err := ResolveNodeDistVersion(want)
	if err != nil {
		return PortableSpec{}, err
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		return PortableSpec{}, fmt.Errorf("unsupported architecture %q", goarch)
	}
	var platform, ext string
	switch goos {
	case "windows":
		platform, ext = "win-"+arch, "zip"
	case "darwin":
		platform, ext = "darwin-"+arch, "tar.gz"
	case "linux":
		platform, ext = "linux-"+arch, "tar.gz"
	default:
		return PortableSpec{}, fmt.Errorf("unsupported OS %q", goos)
	}
	base := fmt.Sprintf("node-v%s-%s.%s", version, platform, ext)
	bins := []string{"node"}
	if goos == "windows" {
		bins = []string{"node.exe"}
	}
	return PortableSpec{
		Kind:        Node,
		Version:     version,
		URL:         fmt.Sprintf("https://nodejs.org/dist/v%s/%s", version, base),
		ChecksumURL: fmt.Sprintf("https://nodejs.org/dist/v%s/SHASUMS256.txt", version),
		Archive:     ext,
		InstallDir:  filepath.Join(toolsDir, "node", version),
		Binaries:    bins,
	}, nil
}

// GitSpec builds the MinGit archive spec. Portable git is only published for
// Windows; elsewhere the caller must instruct the user to install git.
func GitSpec(goos, goarch, toolsDir string) (PortableSpec, error) {
	if goos != "windows" {
		return PortableSpec{}, fmt.Errorf("portable git is not available for %s", goos)
	}
	asset := fmt.Sprintf("MinGit-%s-64-bit.zip", DefaultGitVersion)
	if goarch == "arm64" {
		asset = fmt.Sprintf("MinGit-%s-arm64.zip", DefaultGitVersion)
	}
	tag := "v" + DefaultGitVersion + ".windows.1"
	return PortableSpec{
		Kind:        Git,
		Version:     DefaultGitVersion,
		URL:         fmt.Sprintf("https://github.com/git-for-windows/git/releases/download/%s/%s", tag, asset),
		ChecksumURL: fmt.Sprintf("https://api.github.com/repos/git-for-windows/git/releases/tags/%s", tag),
		Archive:     "zip",
		InstallDir:  filepath.Join(toolsDir, "git", DefaultGitVersion),
		Binaries:    []string{"git.exe"},
	}, nil
}

// NeedsPortableNode reports whether the repository's required Node version
// cannot be satisfied by the detected system Node. A system Node is never
// replaced; a mismatch selects the project-local portable Node instead.
func NeedsPortableNode(system *Tool, want string) bool {
	if system == nil {
		return true
	}
	if strings.TrimSpace(want) == "" {
		return false
	}
	return !MatchWant(want, system.Version)
}

// ReadNodeVersion reads a .node-version file.
func ReadNodeVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(data)), "v"), nil
}

// LockfilePath returns the first lockfile in dir, or "".
func LockfilePath(dir string) string {
	for _, name := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// NpmCacheKey derives the reinstall key from the lockfile bytes and Node
// version, so a lockfile or Node change forces npm ci again.
func NpmCacheKey(lockfile []byte, nodeVersion string) string {
	h := sha256.New()
	h.Write([]byte("node=" + nodeVersion + "\n"))
	h.Write(lockfile)
	return hex.EncodeToString(h.Sum(nil))
}
