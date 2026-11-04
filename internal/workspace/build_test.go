package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

func buildFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hexo := filepath.Join(dir, "node_modules", "hexo", "bin", "hexo")
	if err := os.MkdirAll(filepath.Dir(hexo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hexo, []byte("// hexo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean-search.js"), []byte("// clean"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeArtifacts(t *testing.T, dir string, search string) {
	t.Helper()
	public := filepath.Join(dir, "public")
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "search.json"), []byte(search), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newBuilder(t *testing.T, fake *execx.Fake) (*Builder, *core.Logger) {
	t.Helper()
	logger := core.NewLogger(t.TempDir())
	if err := logger.Begin("workspace"); err != nil {
		t.Fatal(err)
	}
	return &Builder{Runner: fake, Logger: logger, StepID: "workspace", GOOS: "windows"}, logger
}

func TestBuildSuccess(t *testing.T) {
	dir := buildFixture(t)
	writeArtifacts(t, dir, `[{"title":"hello"}]`)
	fake := execx.NewFake()
	b, _ := newBuilder(t, fake)
	ctx := context.Background()
	if err := b.InstallDependencies(ctx, dir, "node", "npm-cli.js"); err != nil {
		t.Fatal(err)
	}
	evidence, err := b.Build(ctx, dir, "node")
	if err != nil {
		t.Fatal(err)
	}
	if evidence["public/index.html"] == "" || evidence["public/search.json"] == "" {
		t.Fatalf("evidence = %v", evidence)
	}
	var seen []string
	for _, call := range fake.Calls {
		seen = append(seen, strings.Join(call.Args, " "))
	}
	joined := strings.Join(seen, "\n")
	for _, want := range []string{"npm-cli.js ci", "clean", "generate", "clean-search.js"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in calls:\n%s", want, joined)
		}
	}
}

func TestNpmInstallFailureKeepsTail(t *testing.T) {
	dir := buildFixture(t)
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		if strings.Contains(strings.Join(c.Args, " "), " ci") {
			return execx.Result{ExitCode: 1, Stderr: "npm ERR! network failure"}
		}
		return execx.Result{}
	}
	b, logger := newBuilder(t, fake)
	err := b.InstallDependencies(context.Background(), dir, "node", "npm-cli.js")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "NPM_INSTALL_FAILED" {
		t.Fatalf("err = %v", err)
	}
	if pe.LogPath == "" {
		t.Fatal("log path missing")
	}
	if !strings.Contains(pe.Details["tail"], "npm ERR!") {
		t.Fatalf("tail = %q", pe.Details["tail"])
	}
	if logger == nil {
		t.Fatal("logger")
	}
}

func TestBuildFailureKeepsTail(t *testing.T) {
	dir := buildFixture(t)
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		if len(c.Args) > 0 && c.Args[len(c.Args)-1] == "generate" {
			return execx.Result{ExitCode: 1, Stderr: "YAMLException: bad config"}
		}
		return execx.Result{}
	}
	b, _ := newBuilder(t, fake)
	_, err := b.Build(context.Background(), dir, "node")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "BUILD_FAILED" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(pe.Details["tail"], "YAMLException") {
		t.Fatalf("tail = %q", pe.Details["tail"])
	}
}

func TestBuildRequiresArtifacts(t *testing.T) {
	dir := buildFixture(t)
	writeArtifacts(t, dir, "")
	b, _ := newBuilder(t, execx.NewFake())
	_, err := b.Build(context.Background(), dir, "node")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "BUILD_FAILED" || !strings.Contains(pe.Message, "search.json") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequiresCleanSearch(t *testing.T) {
	dir := buildFixture(t)
	if err := os.Remove(filepath.Join(dir, "clean-search.js")); err != nil {
		t.Fatal(err)
	}
	b, _ := newBuilder(t, execx.NewFake())
	_, err := b.Build(context.Background(), dir, "node")
	if pe := protocol.As(err); pe == nil || pe.Code != "BUILD_FAILED" {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequiresLocalHexo(t *testing.T) {
	dir := t.TempDir()
	b, _ := newBuilder(t, execx.NewFake())
	_, err := b.Build(context.Background(), dir, "node")
	if pe := protocol.As(err); pe == nil || pe.Code != "BUILD_FAILED" {
		t.Fatalf("err = %v", err)
	}
}
