package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/execx"
)

func TestDetectGitSatisfied(t *testing.T) {
	fake := execx.NewFake()
	fake.LookPaths["git"] = "/usr/bin/git"
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{Stdout: "git version 2.43.0\n"}
	}
	tool, err := DetectGit(context.Background(), fake, "")
	if err != nil {
		t.Fatal(err)
	}
	if tool == nil {
		t.Fatal("git not detected")
	}
	if !GitSupported(tool.Version) {
		t.Fatalf("version %s should be supported", tool.Version)
	}
}

func TestDetectGitTooLow(t *testing.T) {
	fake := execx.NewFake()
	fake.LookPaths["git"] = "/usr/bin/git"
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{Stdout: "git version 2.10.0\n"}
	}
	tool, err := DetectGit(context.Background(), fake, "")
	if err != nil || tool == nil {
		t.Fatalf("tool=%v err=%v", tool, err)
	}
	if GitSupported(tool.Version) {
		t.Fatalf("version %s should be too low", tool.Version)
	}
}

func TestDetectMissing(t *testing.T) {
	fake := execx.NewFake()
	tool, err := DetectGit(context.Background(), fake, "")
	if err != nil {
		t.Fatal(err)
	}
	if tool != nil {
		t.Fatal("missing tool should return nil")
	}
}

func TestDetectNode(t *testing.T) {
	fake := execx.NewFake()
	fake.LookPaths["node"] = "node"
	fake.Handler = func(c execx.Call) execx.Result { return execx.Result{Stdout: "v22.14.0\n"} }
	tool, err := DetectNode(context.Background(), fake, "")
	if err != nil || tool == nil {
		t.Fatalf("tool=%v err=%v", tool, err)
	}
	if !NodeSupported(tool.Version) {
		t.Fatalf("version %s should be supported", tool.Version)
	}
}

func TestDetectNpmViaNode(t *testing.T) {
	dir := t.TempDir()
	nodePath := filepath.Join(dir, "node.exe")
	if err := os.WriteFile(nodePath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsDir := filepath.Join(dir, "node_modules", "npm", "bin")
	if err := os.MkdirAll(jsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	js := filepath.Join(jsDir, "npm-cli.js")
	if err := os.WriteFile(js, []byte("// npm"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		if c.Name == nodePath && len(c.Args) > 0 && c.Args[0] == js {
			return execx.Result{Stdout: "10.9.2\n"}
		}
		return execx.Result{ExitCode: 1, Stderr: "unexpected call"}
	}
	tool, err := DetectNpm(context.Background(), fake, &Tool{Path: nodePath}, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if tool == nil {
		t.Fatal("npm not detected")
	}
	if tool.Path != js {
		t.Fatalf("npm path = %s, want %s", tool.Path, js)
	}
	if tool.Version.String() != "10.9.2" {
		t.Fatalf("version = %s", tool.Version)
	}
}

func TestNpmInvocationUsesNodeForJS(t *testing.T) {
	name, args := NpmInvocation("node.exe", `C:\node\npm-cli.js`, "windows", "ci")
	if name != "node.exe" {
		t.Fatalf("name = %s", name)
	}
	if len(args) != 2 || args[0] != `C:\node\npm-cli.js` || args[1] != "ci" {
		t.Fatalf("args = %v", args)
	}
}

func TestEnvironWithPrependsPath(t *testing.T) {
	env := EnvironWith("/opt/tool/bin")
	found := false
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(key, "PATH") && strings.HasPrefix(value, "/opt/tool/bin") {
			found = true
		}
	}
	if !found {
		t.Fatal("PATH with prepended tool dir missing")
	}
}
