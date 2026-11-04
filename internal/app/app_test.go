package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/core"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
	"github.com/celestezj/blog-bootstrap/internal/run"
)

const testConfig = `version: 1
repos:
  source: git@github.com:celestezj/blog-delta.git
  theme: git@github.com:celestezj/hexo-theme-A4.git
  deploy: git@github.com:celestezj/celestezj.github.io.git
git_identity:
  name: Celeste
  email: celeste@example.com
`

type fakeStep struct {
	id       string
	required bool
	inspect  run.Inspection
	verify   map[string]string
	err      error
}

func (f *fakeStep) ID() string     { return f.id }
func (f *fakeStep) Required() bool { return f.required }
func (f *fakeStep) Inspect(context.Context) (run.Inspection, error) {
	return f.inspect, nil
}
func (f *fakeStep) Apply(context.Context) error { return nil }
func (f *fakeStep) Verify(context.Context) (map[string]string, error) {
	return f.verify, f.err
}

func newTestApp(t *testing.T, step *fakeStep) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "blog-setup.yml")
	if err := os.WriteFile(cfgPath, []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	return &App{
		Version: "test",
		Stdout:  &stdout,
		Stderr:  &stderr,
		Home:    dir,
		GOOS:    "windows",
		GOARCH:  "amd64",
		Runner:  execx.NewFake(),
		HTTP:    &http.Client{Timeout: time.Second},
		Browser: core.BrowserFunc(func(string) error { return nil }),
		Now:     func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) },
		Sleep:   func(time.Duration) {},
		StepsFactory: func(*core.Context) []run.Step {
			return []run.Step{step}
		},
	}, cfgPath
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run UPDATE_GOLDEN=1 go test ./internal/app): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestVerifyExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantExit int
		golden   string
	}{
		{
			name:     "ok",
			wantExit: protocol.ExitOK,
			golden:   "verify_ok.json",
		},
		{
			name: "human",
			err: protocol.Human("AUTH_KEY_NOT_ADDED", "公钥尚未在 GitHub 生效", &protocol.NextAction{
				Type:     "human",
				Code:     "AUTH_KEY_NOT_ADDED",
				OpenURL:  "https://github.com/settings/ssh/new?title=blog-bootstrap",
				Message:  "请在浏览器中确认已登录目标账号，点击 Add SSH key。",
				Expected: "Hi celestezj!",
				Poll: &protocol.Poll{
					Command:         "blog-bootstrap verify --step auth.ssh --json",
					IntervalSeconds: 5,
					TimeoutSeconds:  180,
				},
			}),
			wantExit: protocol.ExitHuman,
			golden:   "exit_2_human.json",
		},
		{
			name:     "retry",
			err:      protocol.Retry("NETWORK_UNREACHABLE", "GitHub 不可达"),
			wantExit: protocol.ExitRetry,
			golden:   "exit_3_retry.json",
		},
		{
			name:     "fatal",
			err:      protocol.Fatal("REPO_CONFLICT", "目标目录属于其他仓库"),
			wantExit: protocol.ExitFatal,
			golden:   "exit_4_fatal.json",
		},
		{
			name:     "verify",
			err:      protocol.Verify("THEME_MISMATCH", "主题 HEAD 与父仓库 gitlink 不一致"),
			wantExit: protocol.ExitVerify,
			golden:   "exit_5_verify.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := &fakeStep{
				id:       "fake",
				required: true,
				verify:   map[string]string{"probe": "ok"},
				err:      tc.err,
			}
			a, cfgPath := newTestApp(t, step)
			exit := a.Run([]string{"verify", "--step", "fake", "--config", cfgPath, "--json"})
			if exit != tc.wantExit {
				t.Fatalf("exit = %d, want %d; output:\n%s", exit, tc.wantExit, a.Stdout.(*bytes.Buffer).String())
			}
			got := a.Stdout.(*bytes.Buffer).String()
			assertGolden(t, tc.golden, got)
			if strings.Contains(got, "PRIVATE KEY") {
				t.Fatal("JSON output leaked a private key")
			}
			var env map[string]interface{}
			if err := json.Unmarshal([]byte(got), &env); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if env["schema_version"].(float64) != 1 {
				t.Fatalf("schema_version = %v", env["schema_version"])
			}
			if env["command"] != "verify" {
				t.Fatalf("command = %v", env["command"])
			}
			if tc.wantExit != protocol.ExitOK {
				if _, ok := env["next_action"].(map[string]interface{}); !ok {
					t.Fatalf("failure without next_action: %s", got)
				}
			}
		})
	}
}

func TestRunRejectsStaleConsent(t *testing.T) {
	step := &fakeStep{
		id:       "fake",
		required: true,
		inspect:  run.Inspection{Satisfied: true, Evidence: map[string]string{"probe": "ok"}},
		verify:   map[string]string{"probe": "ok"},
	}
	a, cfgPath := newTestApp(t, step)
	exit := a.Run([]string{"run", "--consent", "deadbeef", "--config", cfgPath, "--json"})
	if exit != protocol.ExitHuman {
		t.Fatalf("exit = %d, want %d; output:\n%s", exit, protocol.ExitHuman, a.Stdout.(*bytes.Buffer).String())
	}
	out := a.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, `"code": "CONSENT_MISMATCH"`) {
		t.Fatalf("missing CONSENT_MISMATCH: %s", out)
	}
	if !strings.Contains(out, `"type": "human"`) {
		t.Fatalf("missing human next_action: %s", out)
	}
}

func TestVerifyRequiresStrictOrStep(t *testing.T) {
	step := &fakeStep{id: "fake", required: true}
	a, cfgPath := newTestApp(t, step)
	exit := a.Run([]string{"verify", "--config", cfgPath, "--json"})
	if exit != protocol.ExitFatal {
		t.Fatalf("exit = %d, want %d", exit, protocol.ExitFatal)
	}
	if !strings.Contains(a.Stdout.(*bytes.Buffer).String(), `"code": "USAGE"`) {
		t.Fatalf("missing USAGE error: %s", a.Stdout.(*bytes.Buffer).String())
	}
}

func TestUnknownStep(t *testing.T) {
	step := &fakeStep{id: "fake", required: true}
	a, cfgPath := newTestApp(t, step)
	exit := a.Run([]string{"verify", "--step", "nope", "--config", cfgPath, "--json"})
	if exit != protocol.ExitFatal {
		t.Fatalf("exit = %d, want %d", exit, protocol.ExitFatal)
	}
	if !strings.Contains(a.Stdout.(*bytes.Buffer).String(), `"code": "UNKNOWN_STEP"`) {
		t.Fatalf("missing UNKNOWN_STEP: %s", a.Stdout.(*bytes.Buffer).String())
	}
}
