package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// Tool is a resolved executable with its reported version.
type Tool struct {
	Name    string
	Path    string
	Version Version
	Output  string
	Source  string // system | portable
	Extra   map[string]string
}

// ProbeTool locates and runs a version probe. A missing tool is (nil, nil); a
// tool that exists but cannot report a version is an error.
func ProbeTool(ctx context.Context, r execx.Runner, name, explicit string, args ...string) (*Tool, error) {
	path := ""
	if explicit != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			path = explicit
		}
	}
	if path == "" {
		found, err := r.LookPath(name)
		if err != nil {
			return nil, nil
		}
		path = found
	}
	res := r.Run(ctx, "", nil, path, args...)
	if res.Err != nil {
		return nil, protocol.Fatal("TOOL_PROBE_FAILED", "无法执行 "+path+": "+res.Err.Error()).WithCause(res.Err)
	}
	out := res.Combined()
	v, err := ParseVersion(out)
	if err != nil {
		return nil, protocol.Fatal("TOOL_PROBE_FAILED", "无法解析 "+name+" 版本: "+out).WithCause(err)
	}
	return &Tool{Name: name, Path: path, Version: v, Output: out, Source: "system"}, nil
}

// DetectGit probes git, preferring a previously recorded portable path.
func DetectGit(ctx context.Context, r execx.Runner, explicit string) (*Tool, error) {
	t, err := ProbeTool(ctx, r, "git", explicit, "--version")
	if err != nil || t == nil {
		return t, err
	}
	if st, statErr := os.Stat(explicit); explicit != "" && statErr == nil && !st.IsDir() {
		t.Source = "portable"
	}
	return t, nil
}

// DetectNode probes node.
func DetectNode(ctx context.Context, r execx.Runner, explicit string) (*Tool, error) {
	t, err := ProbeTool(ctx, r, "node", explicit, "-v")
	if err != nil || t == nil {
		return t, err
	}
	if st, statErr := os.Stat(explicit); explicit != "" && statErr == nil && !st.IsDir() {
		t.Source = "portable"
	}
	return t, nil
}

// DetectSSH probes ssh, which writes its version to stderr.
func DetectSSH(ctx context.Context, r execx.Runner, explicit string) (*Tool, error) {
	return ProbeTool(ctx, r, "ssh", explicit, "-V")
}

// NPMMain resolves npm's JavaScript entry point relative to node. Using
// node.exe + npm-cli.js avoids Windows .cmd shims, which CreateProcess cannot
// execute directly.
func NPMMain(nodePath string) (string, bool) {
	dir := filepath.Dir(nodePath)
	candidates := []string{
		filepath.Join(dir, "node_modules", "npm", "bin", "npm-cli.js"),
		filepath.Join(dir, "..", "lib", "node_modules", "npm", "bin", "npm-cli.js"),
		filepath.Join(dir, "..", "lib", "npm", "bin", "npm-cli.js"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, aerr := filepath.Abs(c)
			if aerr == nil {
				return abs, true
			}
			return c, true
		}
	}
	return "", false
}

// DetectNpm resolves npm. It prefers node + npm-cli.js; on POSIX it falls back
// to the npm on PATH.
func DetectNpm(ctx context.Context, r execx.Runner, node *Tool, goos string) (*Tool, error) {
	if node != nil {
		if js, ok := NPMMain(node.Path); ok {
			res := r.Run(ctx, "", nil, node.Path, js, "-v")
			if res.Err == nil {
				if v, err := ParseVersion(res.Combined()); err == nil {
					return &Tool{Name: "npm", Path: js, Version: v, Output: res.Combined(), Source: node.Source}, nil
				}
			}
		}
	}
	if goos == "windows" {
		return nil, nil
	}
	t, err := ProbeTool(ctx, r, "npm", "", "-v")
	if err != nil || t == nil {
		return t, err
	}
	return t, nil
}

// NpmInvocation returns the argv to execute npm with the given arguments.
func NpmInvocation(nodePath, npmMain string, goos string, args ...string) (string, []string) {
	if npmMain != "" && len(npmMain) > 3 && npmMain[len(npmMain)-3:] == ".js" {
		return nodePath, append([]string{npmMain}, args...)
	}
	if goos == "windows" {
		return nodePath, append([]string{npmMain}, args...)
	}
	return npmMain, args
}

// EnvironWith builds a process environment with extra directories prepended to
// PATH so freshly installed tools work even when the parent PATH is stale.
func EnvironWith(extraDirs ...string) []string {
	env := os.Environ()
	pathKey := "PATH"
	sep := string(os.PathListSeparator)
	if runtime.GOOS == "windows" {
		pathKey = "Path"
	}
	current := os.Getenv("PATH")
	var prefix []string
	for _, d := range extraDirs {
		if d != "" {
			prefix = append(prefix, d)
		}
	}
	joined := ""
	for _, p := range prefix {
		if joined == "" {
			joined = p
		} else {
			joined += sep + p
		}
	}
	if current != "" {
		if joined != "" {
			joined += sep + current
		} else {
			joined = current
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if strings.EqualFold(key, pathKey) || strings.EqualFold(key, "PATH") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, pathKey+"="+joined)
}
