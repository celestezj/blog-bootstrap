package steps

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/state"
)

// The global CLI reports "hexo-cli: 4.3.2"; verification must accept it even
// though build uses the project-local hexo 7.x.
func TestHexoVerifyAcceptsHexoCLI4(t *testing.T) {
	root := t.TempDir()
	pkgRoot := filepath.Join(root, "npm-root")
	bin := filepath.Join(pkgRoot, "hexo-cli", "bin", "hexo")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/usr/bin/env node\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := execx.NewFake()
	fake.LookPaths["node"] = "/usr/bin/node"
	fake.LookPaths["npm"] = "/usr/bin/npm"
	fake.Handler = func(c execx.Call) execx.Result {
		switch {
		case c.Name == "/usr/bin/node" && len(c.Args) == 1 && c.Args[0] == "-v":
			return execx.Result{Stdout: "v22.14.0"}
		case c.Name == "/usr/bin/npm" && len(c.Args) == 1 && c.Args[0] == "-v":
			return execx.Result{Stdout: "10.9.0"}
		case c.Name == "/usr/bin/npm" && len(c.Args) == 2 && c.Args[0] == "root" && c.Args[1] == "-g":
			return execx.Result{Stdout: pkgRoot}
		case c.Name == "/usr/bin/node" && len(c.Args) == 2 && c.Args[1] == "version":
			return execx.Result{Stdout: "hexo-cli: 4.3.2"}
		}
		return execx.Result{ExitCode: 1}
	}

	clock := func() time.Time { return time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC) }
	ctx := &core.Context{
		Root:   root,
		Config: &config.Resolved{Dir: filepath.Join(root, "blog"), NodeWant: "22", Strategy: "system_first"},
		State:  state.New(filepath.Join(root, ".bootstrap", "state.json"), clock),
		Runner: fake,
		GOOS:   "linux",
		Now:    clock,
		Sleep:  func(time.Duration) {},
	}
	nodes := &NodeStep{Ctx: ctx}
	step := &HexoStep{Ctx: ctx, Nodes: nodes}

	insp, err := step.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !insp.Satisfied {
		t.Fatalf("hexo-cli 4.x should satisfy the optional step: %+v", insp)
	}
	evidence, err := step.Verify(context.Background())
	if err != nil {
		t.Fatalf("verify should accept hexo-cli 4.x: %v", err)
	}
	if evidence["hexo_version"] != "4.3.2" {
		t.Fatalf("hexo_version = %q", evidence["hexo_version"])
	}
}
