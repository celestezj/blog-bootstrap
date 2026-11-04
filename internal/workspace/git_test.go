package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=protocol.file.allow",
		"GIT_CONFIG_VALUE_0=always",
	)
}

func gitPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required for workspace tests")
	}
	return p
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "master")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func newGit(t *testing.T) *Git {
	return &Git{Runner: execx.Exec{}, Path: gitPath(t), Env: gitEnv()}
}

func cloneFixture(t *testing.T, src, branch string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "--branch", branch, src, dst)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone fixture: %v\n%s", err, out)
	}
	return dst
}

func TestInspectTargetReportsMissing(t *testing.T) {
	g := newGit(t)
	st, err := g.InspectTarget(context.Background(), filepath.Join(t.TempDir(), "absent"), "https://github.com/a/b.git")
	if err != nil {
		t.Fatal(err)
	}
	if st.Exists {
		t.Fatalf("state = %+v, want absent", st)
	}
}

func TestInspectTargetRejectsNonRepo(t *testing.T) {
	g := newGit(t)
	dir := t.TempDir()
	_, err := g.InspectTarget(context.Background(), dir, "https://github.com/a/b.git")
	if code := protocol.As(err).Code; code != "REPO_CONFLICT" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}

func TestInspectTargetMatchingRemote(t *testing.T) {
	g := newGit(t)
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "git@celestezj:alice/blog-delta.git")
	st, err := g.InspectTarget(context.Background(), dir, "https://github.com/alice/blog-delta.git")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Remote == "" || st.Head == "" {
		t.Fatalf("state = %+v", st)
	}
}

func TestInspectTargetReportsConflict(t *testing.T) {
	g := newGit(t)
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://github.com/alice/other.git")
	_, err := g.InspectTarget(context.Background(), dir, "https://github.com/alice/blog-delta.git")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "REPO_CONFLICT" {
		t.Fatalf("err = %v", err)
	}
	if pe.Details["actual"] != "https://github.com/alice/other.git" {
		t.Fatalf("details = %v", pe.Details)
	}
}

func TestEnsureCheckoutSkipsCloneForMatchingRepo(t *testing.T) {
	g := newGit(t)
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://github.com/alice/blog-delta.git")
	marker := filepath.Join(dir, "keep-me.txt")
	if err := os.WriteFile(marker, []byte("existing work"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence, err := g.EnsureCheckout(context.Background(), "https://github.com/alice/blog-delta.git", dir, "master")
	if err != nil {
		t.Fatal(err)
	}
	if evidence["branch"] != "master" {
		t.Fatalf("evidence = %v", evidence)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("existing checkout must be preserved")
	}
}

func TestEnsureCheckoutRejectsWrongBranch(t *testing.T) {
	g := newGit(t)
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://github.com/alice/blog-delta.git")
	_, err := g.EnsureCheckout(context.Background(), "https://github.com/alice/blog-delta.git", dir, "main")
	if code := protocol.As(err).Code; code != "BRANCH_MISMATCH" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}

func TestCloneAndVerifyCheckout(t *testing.T) {
	g := newGit(t)
	src := newRepo(t)
	target := filepath.Join(t.TempDir(), "work")
	if err := g.Clone(context.Background(), src, target, "master"); err != nil {
		t.Fatal(err)
	}
	evidence, err := g.VerifyCheckout(context.Background(), target, src, "master")
	if err != nil {
		t.Fatal(err)
	}
	if evidence["branch"] != "master" || evidence["head"] == "" {
		t.Fatalf("evidence = %v", evidence)
	}
}

func TestVerifyCheckoutRejectsWrongRemote(t *testing.T) {
	g := newGit(t)
	src := newRepo(t)
	target := cloneFixture(t, src, "master")
	_, err := g.VerifyCheckout(context.Background(), target, "https://github.com/alice/blog-delta.git", "master")
	if code := protocol.As(err).Code; code != "REMOTE_MISMATCH" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}

func TestVerifyCheckoutRejectsWrongBranch(t *testing.T) {
	g := newGit(t)
	src := newRepo(t)
	target := cloneFixture(t, src, "master")
	_, err := g.VerifyCheckout(context.Background(), target, src, "main")
	if code := protocol.As(err).Code; code != "BRANCH_MISMATCH" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}

func TestCloneMissingBranch(t *testing.T) {
	g := newGit(t)
	src := newRepo(t)
	err := g.Clone(context.Background(), src, filepath.Join(t.TempDir(), "work"), "missing")
	if code := protocol.As(err).Code; code != "BRANCH_NOT_FOUND" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}

func TestCloneNetworkFailureIsRetryable(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(execx.Call) execx.Result {
		return execx.Result{ExitCode: 128, Stderr: "fatal: unable to access: Connection timed out"}
	}
	g := &Git{Runner: fake, Path: "git"}
	err := g.Clone(context.Background(), "https://github.com/a/b.git", filepath.Join(t.TempDir(), "w"), "master")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "NETWORK_UNREACHABLE" || pe.Class != protocol.ClassRetry {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmoduleLifecycle(t *testing.T) {
	ctx := context.Background()
	g := newGit(t)
	theme := newRepo(t)
	parent := newRepo(t)
	runGit(t, parent, "submodule", "add", theme, "themes/A4")
	runGit(t, parent, "commit", "-am", "add theme")

	path, ok, err := g.SubmodulePath(ctx, parent, "themes/A4")
	if err != nil || !ok || path != "themes/A4" {
		t.Fatalf("path=%q ok=%v err=%v", path, ok, err)
	}
	gitmodulesBefore, err := os.ReadFile(filepath.Join(parent, ".gitmodules"))
	if err != nil {
		t.Fatal(err)
	}
	override := "git@celestezj:alice/hexo-theme-A4.git"
	if err := g.ConfigureSubmoduleURL(ctx, parent, "themes/A4", override); err != nil {
		t.Fatal(err)
	}
	got := runGit(t, parent, "config", "submodule.themes/A4.url")
	if got != override {
		t.Fatalf("url = %q", got)
	}
	gitmodulesAfter, err := os.ReadFile(filepath.Join(parent, ".gitmodules"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gitmodulesBefore) != string(gitmodulesAfter) {
		t.Fatal(".gitmodules must not be modified")
	}

	if err := g.InitSubmodules(ctx, parent); err != nil {
		t.Fatal(err)
	}
	evidence, err := g.VerifySubmodule(ctx, parent, "themes/A4")
	if err != nil {
		t.Fatal(err)
	}
	if evidence["theme_head"] != evidence["gitlink"] {
		t.Fatalf("evidence = %v", evidence)
	}

	themeWork := filepath.Join(parent, "themes", "A4")
	runGit(t, themeWork, "commit", "--allow-empty", "-m", "drift")
	_, err = g.VerifySubmodule(ctx, parent, "themes/A4")
	if code := protocol.As(err).Code; code != "SUBMODULE_FAILED" {
		t.Fatalf("drift code = %s, err = %v", code, err)
	}
}

func TestVerifySubmoduleRejectsUninitialized(t *testing.T) {
	ctx := context.Background()
	g := newGit(t)
	theme := newRepo(t)
	parent := newRepo(t)
	runGit(t, parent, "submodule", "add", theme, "themes/A4")
	runGit(t, parent, "commit", "-am", "add theme")
	clone := cloneFixture(t, parent, "master")
	_, err := g.VerifySubmodule(ctx, clone, "themes/A4")
	if code := protocol.As(err).Code; code != "SUBMODULE_FAILED" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}

func TestInitSubmodulesRetriesOnceThenFails(t *testing.T) {
	fake := execx.NewFake()
	calls := 0
	fake.Handler = func(execx.Call) execx.Result {
		calls++
		return execx.Result{ExitCode: 1, Stderr: "unable to init"}
	}
	g := &Git{Runner: fake, Path: "git"}
	err := g.InitSubmodules(context.Background(), t.TempDir())
	if code := protocol.As(err).Code; code != "SUBMODULE_FAILED" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestCloneThemeWhenNoSubmoduleDeclared(t *testing.T) {
	ctx := context.Background()
	g := newGit(t)
	theme := newRepo(t)
	parent := newRepo(t)
	if err := os.MkdirAll(filepath.Join(parent, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, parent, "clone", theme, "themes/A4")
	runGit(t, parent, "add", "themes/A4")
	runGit(t, parent, "commit", "-m", "gitlink only")

	if _, ok, err := g.SubmodulePath(ctx, parent, "themes/A4"); err != nil || ok {
		t.Fatalf("no .gitmodules expected: ok=%v err=%v", ok, err)
	}
	gitlink, err := g.ThemeGitlink(ctx, parent, ThemePath)
	if err != nil {
		t.Fatal(err)
	}
	work := cloneFixture(t, parent, "master")
	themeDir := filepath.Join(work, ThemePath)
	if err := g.CloneTheme(ctx, theme, themeDir, gitlink); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, themeDir, "rev-parse", "HEAD"); got != gitlink {
		t.Fatalf("theme HEAD = %s, want %s", got, gitlink)
	}
}

func TestCloneThemeRejectsWrongCommit(t *testing.T) {
	ctx := context.Background()
	g := newGit(t)
	theme := newRepo(t)
	themeDir := cloneFixture(t, theme, "master")
	runGit(t, themeDir, "commit", "--allow-empty", "-m", "advance")
	err := g.CloneTheme(ctx, theme, themeDir, "0000000000000000000000000000000000000000")
	if code := protocol.As(err).Code; code != "THEME_MISMATCH" {
		t.Fatalf("code = %s, err = %v", code, err)
	}
}
