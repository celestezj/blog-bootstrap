package steps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/state"
	"github.com/celestezj/blog-bootstrap/internal/toolchain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testContext(t *testing.T, dir string, runner execx.Runner) *core.Context {
	t.Helper()
	store, err := state.Open(filepath.Join(dir, ".bootstrap", "state.json"), func() time.Time {
		return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &core.Context{
		Root: dir,
		Home: dir,
		Config: &config.Resolved{
			Dir:      dir,
			NodeWant: "22",
			Strategy: "system_first",
			BaseDir:  dir,
		},
		State:  store,
		Runner: runner,
		Logger: core.NewLogger(filepath.Join(dir, ".bootstrap", "logs")),
		GOOS:   "windows",
		GOARCH: "amd64",
		Now:    func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) },
		Sleep:  func(time.Duration) {},
	}
}

func fakeNodeInstall(t *testing.T, dir, version string) string {
	t.Helper()
	node := filepath.Join(dir, "node.exe")
	writeFile(t, node, "")
	writeFile(t, filepath.Join(dir, "node_modules", "npm", "bin", "npm-cli.js"), "")
	return node
}

func nodeRunner(systemNode, systemVersion, portableNode, portableVersion string) *execx.Fake {
	fake := execx.NewFake()
	fake.LookPaths["node"] = systemNode
	fake.Handler = func(call execx.Call) execx.Result {
		if len(call.Args) > 0 && strings.HasSuffix(call.Args[0], "npm-cli.js") && call.Args[len(call.Args)-1] == "-v" {
			return execx.Result{Stdout: "10.8.2\n"}
		}
		switch call.Name {
		case systemNode:
			return execx.Result{Stdout: "v" + systemVersion + "\n"}
		case portableNode:
			return execx.Result{Stdout: "v" + portableVersion + "\n"}
		}
		return execx.Result{}
	}
	return fake
}

func newNodeStep(t *testing.T, ctx *core.Context) *NodeStep {
	t.Helper()
	installer := toolchain.NewInstaller(ctx.Runner)
	installer.GOOS = ctx.GOOS
	return &NodeStep{
		Ctx:       ctx,
		Installer: installer,
		Portable: &toolchain.Portable{
			ToolsDir: ctx.ToolsDir(),
			GOOS:     ctx.GOOS,
			GOARCH:   ctx.GOARCH,
		},
	}
}

func TestDesiredVersionPrefersRepositoryFile(t *testing.T) {
	dir := t.TempDir()
	ctx := testContext(t, dir, execx.NewFake())
	step := newNodeStep(t, ctx)
	if got := step.DesiredVersion(); got != "22" {
		t.Fatalf("DesiredVersion without file = %q", got)
	}
	writeFile(t, filepath.Join(dir, ".node-version"), "20.18.1\n")
	if got := step.DesiredVersion(); got != "20.18.1" {
		t.Fatalf("DesiredVersion with file = %q", got)
	}
}

func TestMismatchedSystemNodeSelectsPortable(t *testing.T) {
	dir := t.TempDir()
	ctx := testContext(t, dir, nil)
	writeFile(t, filepath.Join(dir, ".node-version"), "20.18.1\n")

	systemNode := fakeNodeInstall(t, filepath.Join(dir, "system"), "22.14.0")
	spec, err := toolchain.NodeSpec("windows", "amd64", "20.18.1", ctx.ToolsDir())
	if err != nil {
		t.Fatal(err)
	}
	portableNode := fakeNodeInstall(t, spec.InstallDir, "20.18.1")
	ctx.Runner = nodeRunner(systemNode, "22.14.0", portableNode, "20.18.1")
	if err := ctx.State.SetPaths(func(p *state.Paths) { p.Node = systemNode }); err != nil {
		t.Fatal(err)
	}

	step := newNodeStep(t, ctx)
	rt, ok, err := step.current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rt == nil {
		t.Fatal("expected a usable runtime")
	}
	if rt.Node.Source != "portable" {
		t.Fatalf("source = %q, want portable", rt.Node.Source)
	}
	if rt.Node.Version.String() != "20.18.1" {
		t.Fatalf("version = %s", rt.Node.Version.String())
	}
}

func TestPreviewReinstallsWhenLockfileChanges(t *testing.T) {
	dir := t.TempDir()
	ctx := testContext(t, dir, nil)
	nodePath := fakeNodeInstall(t, filepath.Join(dir, "node"), "22.14.0")
	writeFile(t, filepath.Join(dir, "node_modules", "hexo", "bin", "hexo"), "")
	writeFile(t, filepath.Join(dir, "public", "index.html"), "<html></html>")
	writeFile(t, filepath.Join(dir, "public", "search.json"), "{}")
	writeFile(t, filepath.Join(dir, "clean-search.js"), "")
	lockPath := filepath.Join(dir, "package-lock.json")
	writeFile(t, lockPath, `{"lockfileVersion":3}`)
	if err := ctx.State.SetPaths(func(p *state.Paths) { p.Node = nodePath }); err != nil {
		t.Fatal(err)
	}

	ciCalls := 0
	fake := execx.NewFake()
	fake.LookPaths["node"] = nodePath
	fake.Handler = func(call execx.Call) execx.Result {
		if len(call.Args) > 0 && strings.HasSuffix(call.Args[0], "npm-cli.js") {
			if len(call.Args) > 1 && call.Args[1] == "ci" {
				ciCalls++
			}
			return execx.Result{Stdout: "10.8.2\n"}
		}
		if call.Name == nodePath {
			if len(call.Args) > 0 && call.Args[0] == "-v" {
				return execx.Result{Stdout: "v22.14.0\n"}
			}
			return execx.Result{}
		}
		return execx.Result{}
	}
	ctx.Runner = fake

	nodes := newNodeStep(t, ctx)
	preview := &PreviewStep{Ctx: ctx, Nodes: nodes}
	key := toolchain.NpmCacheKey([]byte(`{"lockfileVersion":3}`), "22.14.0")
	if err := ctx.State.SetNPMCacheKey(key); err != nil {
		t.Fatal(err)
	}

	insp, err := preview.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !insp.Satisfied {
		t.Fatalf("expected cache hit; actions = %+v", insp.Actions)
	}

	writeFile(t, lockPath, `{"lockfileVersion":3,"changed":true}`)
	insp, err = preview.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if insp.Satisfied {
		t.Fatal("lockfile change must invalidate the install")
	}
	hasCI := false
	for _, a := range insp.Actions {
		if a.Kind == "npm_ci" {
			hasCI = true
		}
	}
	if !hasCI {
		t.Fatalf("expected npm_ci action, got %+v", insp.Actions)
	}

	if err := preview.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ciCalls != 1 {
		t.Fatalf("npm ci calls = %d, want 1", ciCalls)
	}
	newKey := toolchain.NpmCacheKey([]byte(`{"lockfileVersion":3,"changed":true}`), "22.14.0")
	if got := ctx.State.Snapshot().NPMCacheKey; got != newKey {
		t.Fatalf("cache key not updated: %q", got)
	}

	if err := preview.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ciCalls != 1 {
		t.Fatalf("unchanged lockfile must skip npm ci; calls = %d", ciCalls)
	}
}
