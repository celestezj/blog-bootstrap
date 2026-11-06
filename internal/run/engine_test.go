package run_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
	"github.com/celestezj/blog-bootstrap/internal/state"
)

type counters struct {
	inspect, apply, verify int
}

type fakeStep struct {
	id       string
	required bool
	satisfy  bool
	inspectE error
	applyE   error
	verifyE  error
	c        *counters
}

func (f *fakeStep) ID() string     { return f.id }
func (f *fakeStep) Required() bool { return f.required }
func (f *fakeStep) Inspect(context.Context) (run.Inspection, error) {
	f.c.inspect++
	return run.Inspection{Satisfied: f.satisfy}, f.inspectE
}
func (f *fakeStep) Apply(context.Context) error { f.c.apply++; return f.applyE }
func (f *fakeStep) Verify(context.Context) (map[string]string, error) {
	f.c.verify++
	return map[string]string{"ok": "1"}, f.verifyE
}

func newCtx(t *testing.T) (*core.Context, *[]time.Duration) {
	t.Helper()
	root := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	st := state.New(filepath.Join(root, ".bootstrap", "state.json"), clock)
	var waits []time.Duration
	ctx := &core.Context{
		Root:     root,
		Config:   &config.Resolved{Owner: "celestezj", Alias: "celestezj", Dir: filepath.Join(root, "blog")},
		State:    st,
		Now:      clock,
		Sleep:    func(d time.Duration) { waits = append(waits, d) },
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		PlanHash: "hash",
	}
	return ctx, &waits
}

func TestRunPrintsProgressLines(t *testing.T) {
	ctx, _ := newCtx(t)
	var lines []string
	ctx.Progress = func(line string) { lines = append(lines, line) }
	c := &counters{}
	step := &fakeStep{id: "node", required: true, c: c}
	if _, err := run.NewEngine(ctx, step).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"开始执行安装计划：共 1 步",
		"[1/1] node",
		"执行中...",
		"验证通过",
		"正在写入收据",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress missing %q:\n%s", want, joined)
		}
	}
}

func TestVerifyStrictPrintsProgressLines(t *testing.T) {
	ctx, _ := newCtx(t)
	var lines []string
	ctx.Progress = func(line string) { lines = append(lines, line) }
	c := &counters{}
	if _, err := run.NewEngine(ctx, &fakeStep{id: "preview", required: true, c: c}).VerifyStrict(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"验证 1 个步骤", "[1/1] preview", "验证通过"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress missing %q:\n%s", want, joined)
		}
	}
}

func TestRunHappyPathWritesReceipt(t *testing.T) {
	ctx, _ := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "auth.ssh", required: true, c: c}
	eng := run.NewEngine(ctx, step)
	rep, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Receipt == nil {
		t.Fatal("receipt not written")
	}
	if got := ctx.State.Snapshot().Steps["auth.ssh"].Status; got != state.StatusVerified {
		t.Fatalf("state = %s", got)
	}
	if c.apply != 1 {
		t.Fatalf("apply count = %d", c.apply)
	}
}

func TestSatisfiedSkipsApply(t *testing.T) {
	ctx, _ := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "git", required: true, satisfy: true, c: c}
	if _, err := run.NewEngine(ctx, step).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.apply != 0 {
		t.Fatalf("apply should be skipped, got %d calls", c.apply)
	}
}

func TestVerifyFailureWritesNoReceipt(t *testing.T) {
	ctx, _ := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "preview", required: true, c: c, verifyE: protocol.Verify("BUILD_FAILED", "missing search.json")}
	_, err := run.NewEngine(ctx, step).Run(context.Background())
	if err == nil {
		t.Fatal("expected verify failure")
	}
	pe := protocol.As(err)
	if pe.ExitCode() != protocol.ExitVerify {
		t.Fatalf("exit code = %d, want 5", pe.ExitCode())
	}
	if ctx.State.Snapshot().Receipt != nil {
		t.Fatal("receipt must not be written when a probe fails")
	}
}

func TestRetryLimitAndBackoff(t *testing.T) {
	ctx, waits := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "node", required: true, c: c, applyE: protocol.Retry("NETWORK_UNREACHABLE", "timeout")}
	eng := run.NewEngine(ctx, step)
	_, err := eng.Run(context.Background())
	if err == nil {
		t.Fatal("expected failure")
	}
	if c.apply != 4 {
		t.Fatalf("apply count = %d, want 1 initial + 3 retries", c.apply)
	}
	if len(*waits) != 3 {
		t.Fatalf("waits = %v, want 3 backoffs", *waits)
	}
	pe := protocol.As(err)
	if pe.ExitCode() != protocol.ExitHuman {
		t.Fatalf("exhausted network retries should hand off to a human, exit=%d", pe.ExitCode())
	}
}

func TestDeterministicFailureNotRetried(t *testing.T) {
	ctx, waits := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "auth.ssh", required: true, c: c,
		applyE: protocol.Human("AUTH_WRONG_ACCOUNT", "key belongs to another account", nil)}
	_, err := run.NewEngine(ctx, step).Run(context.Background())
	if err == nil {
		t.Fatal("expected failure")
	}
	if c.apply != 1 {
		t.Fatalf("apply count = %d, want 1 (deterministic failures are not retried)", c.apply)
	}
	if len(*waits) != 0 {
		t.Fatalf("unexpected backoff waits: %v", *waits)
	}
}

func TestFailureInvalidatesDownstream(t *testing.T) {
	ctx, _ := newCtx(t)
	c1, c2 := &counters{}, &counters{}
	first := &fakeStep{id: "node", required: true, c: c1, verifyE: protocol.Verify("VERSION_MISMATCH", "bad node")}
	second := &fakeStep{id: "workspace", required: true, c: c2}
	_, err := run.NewEngine(ctx, first, second).Run(context.Background())
	if err == nil {
		t.Fatal("expected failure")
	}
	snap := ctx.State.Snapshot()
	if snap.Steps["workspace"].Status != state.StatusInvalid {
		t.Fatalf("downstream = %s, want invalid", snap.Steps["workspace"].Status)
	}
	if c2.verify != 0 {
		t.Fatal("downstream must not run after an upstream failure")
	}
}

func TestStrictVerifyDoesNotApply(t *testing.T) {
	ctx, _ := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "preview", required: true, c: c}
	rep, err := run.NewEngine(ctx, step).VerifyStrict(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.apply != 0 || c.inspect != 0 {
		t.Fatalf("strict verify must not inspect/apply: %+v", c)
	}
	if rep.Receipt == nil {
		t.Fatal("strict verify should write receipt when probes pass")
	}
}

func TestStrictVerifyPropagatesVerifyCode(t *testing.T) {
	ctx, _ := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "preview", required: true, c: c, verifyE: protocol.Verify("SERVE_FAILED", "no 200")}
	_, err := run.NewEngine(ctx, step).VerifyStrict(context.Background())
	pe := protocol.As(err)
	if pe == nil || pe.ExitCode() != protocol.ExitVerify {
		t.Fatalf("err = %v", err)
	}
}

func TestOptionalStepFailureDoesNotBlock(t *testing.T) {
	ctx, _ := newCtx(t)
	co, cr := &counters{}, &counters{}
	optional := &fakeStep{id: "hexo", required: false, c: co, verifyE: errors.New("global npm prefix not writable")}
	required := &fakeStep{id: "node", required: true, c: cr}
	rep, err := run.NewEngine(ctx, optional, required).Run(context.Background())
	if err != nil {
		t.Fatalf("optional failure must not block: %v", err)
	}
	if rep.Receipt == nil {
		t.Fatal("receipt expected")
	}
	if len(rep.Warnings) == 0 {
		t.Fatal("expected a warning for the optional step")
	}
}

func TestOptionalInspectErrorDoesNotBlock(t *testing.T) {
	ctx, _ := newCtx(t)
	co, cr := &counters{}, &counters{}
	optional := &fakeStep{id: "hexo", required: false, c: co, inspectE: errors.New("probe failed")}
	required := &fakeStep{id: "node", required: true, c: cr}
	rep, err := run.NewEngine(ctx, optional, required).Run(context.Background())
	if err != nil {
		t.Fatalf("optional inspect failure must not block: %v", err)
	}
	if rep.Receipt == nil {
		t.Fatal("receipt expected")
	}
	if len(rep.Warnings) == 0 {
		t.Fatal("expected a warning for the optional step")
	}
	if ctx.State.Snapshot().Steps["hexo"].Status != state.StatusSkipped {
		t.Fatal("optional step should be skipped")
	}
}

func TestInspectErrorStops(t *testing.T) {
	ctx, _ := newCtx(t)
	c := &counters{}
	step := &fakeStep{id: "auth.ssh", required: true, c: c, inspectE: protocol.Fatal("TOOLS_MISSING", "no ssh")}
	_, err := run.NewEngine(ctx, step).Run(context.Background())
	if protocol.As(err).Code != "TOOLS_MISSING" {
		t.Fatalf("err = %v", err)
	}
	if c.apply != 0 {
		t.Fatal("apply must not run when inspection fails")
	}
}
