package sshauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

func TestEvaluateGreeting(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		owner   string
		state   GreetingState
		account string
	}{
		{"match", "Hi celestezj! You've successfully authenticated", "celestezj", GreetingMatch, "celestezj"},
		{"crlf", "Hi celestezj!\r\n", "celestezj", GreetingMatch, "celestezj"},
		{"case", "hi CELESTEZJ !", "celestezj", GreetingMatch, "CELESTEZJ"},
		{"wrong", "Hi muggledy! You've successfully authenticated", "celestezj", GreetingWrong, "muggledy"},
		{"none", "Permission denied (publickey).", "celestezj", GreetingNone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, account := EvaluateGreeting(tt.out, tt.owner)
			if state != tt.state || account != tt.account {
				t.Fatalf("got %s/%s, want %s/%s", state, account, tt.state, tt.account)
			}
		})
	}
}

func manager(fake *execx.Fake) *Manager {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Manager{
		Runner:       fake,
		SSHPath:      "ssh",
		GitPath:      "git",
		Owner:        "celestezj",
		Alias:        "celestezj",
		Now:          func() time.Time { return clock },
		Sleep:        func(d time.Duration) { clock = clock.Add(d) },
		PollInterval: 5 * time.Second,
		PollTimeout:  15 * time.Second,
	}
}

func TestFindReusableKeySkipsWrongAccount(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		switch {
		case strings.Contains(strings.Join(c.Args, " "), "id_old"):
			return execx.Result{ExitCode: 1, Stderr: "Hi muggledy! You've successfully authenticated"}
		case strings.Contains(strings.Join(c.Args, " "), "id_new"):
			return execx.Result{ExitCode: 1, Stderr: "Hi celestezj! You've successfully authenticated"}
		}
		return execx.Result{ExitCode: 255, Stderr: "Permission denied (publickey)."}
	}
	m := manager(fake)
	keys := []Key{{Path: "id_old"}, {Path: "id_new"}}
	got, wrong, err := m.FindReusableKey(context.Background(), keys, Endpoint{"github.com", 22})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Path != "id_new" {
		t.Fatalf("key = %+v", got)
	}
	if len(wrong) != 1 || wrong[0] != "muggledy" {
		t.Fatalf("wrong = %v", wrong)
	}
}

func TestGreetingIgnoresExitCodeOne(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{ExitCode: 1, Stderr: "Hi celestezj! You've successfully authenticated"}
	}
	m := manager(fake)
	out, err := m.Greeting(context.Background(), Key{Path: "id"}, Endpoint{"github.com", 22})
	if err != nil {
		t.Fatal(err)
	}
	if state, _ := EvaluateGreeting(out, "celestezj"); state != GreetingMatch {
		t.Fatalf("state = %s", state)
	}
}

func TestPollEnrollmentSuccess(t *testing.T) {
	calls := 0
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		calls++
		if calls < 3 {
			return execx.Result{ExitCode: 255, Stderr: "Permission denied (publickey)."}
		}
		return execx.Result{ExitCode: 1, Stderr: "Hi celestezj! You've successfully authenticated"}
	}
	m := manager(fake)
	state, account, err := m.PollEnrollment(context.Background(), Key{Path: "id"}, Endpoint{"github.com", 22})
	if err != nil {
		t.Fatal(err)
	}
	if state != GreetingMatch || account != "celestezj" {
		t.Fatalf("state=%s account=%s", state, account)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestPollEnrollmentTimeout(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{ExitCode: 255, Stderr: "Permission denied (publickey)."}
	}
	m := manager(fake)
	state, _, err := m.PollEnrollment(context.Background(), Key{Path: "id"}, Endpoint{"github.com", 22})
	if err != nil {
		t.Fatal(err)
	}
	if state != GreetingNone {
		t.Fatalf("state = %s", state)
	}
}

func TestPollEnrollmentWrongAccount(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{ExitCode: 1, Stderr: "Hi muggledy! You've successfully authenticated"}
	}
	m := manager(fake)
	state, account, err := m.PollEnrollment(context.Background(), Key{Path: "id"}, Endpoint{"github.com", 22})
	if err != nil {
		t.Fatal(err)
	}
	if state != GreetingWrong || account != "muggledy" {
		t.Fatalf("state=%s account=%s", state, account)
	}
}

func TestChooseEndpointFallsBackTo443(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		if strings.HasSuffix(c.Args[len(c.Args)-1], "ssh.github.com") {
			return execx.Result{ExitCode: 255, Stderr: "Permission denied (publickey)."}
		}
		return execx.Result{ExitCode: 255, Stderr: "ssh: connect to host github.com port 22: Connection timed out"}
	}
	m := manager(fake)
	ep, err := m.ChooseEndpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ep.Host != "ssh.github.com" || ep.Port != 443 {
		t.Fatalf("endpoint = %+v", ep)
	}
}

func TestChooseEndpointPrefers22(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		return execx.Result{ExitCode: 255, Stderr: "Permission denied (publickey)."}
	}
	m := manager(fake)
	ep, err := m.ChooseEndpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ep.Port != 22 {
		t.Fatalf("endpoint = %+v", ep)
	}
}

func TestProbeWriteUsesDryRunAndKeepsIdentityLocal(t *testing.T) {
	fake := execx.NewFake()
	m := manager(fake)
	err := m.ProbeWrite(context.Background(), "git@celestezj:celestezj/celestezj.github.io.git", "master", "Celeste", "c@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range fake.Calls {
		joined := strings.Join(c.Args, " ")
		if strings.HasPrefix(joined, "push --dry-run") {
			found = true
			if !strings.Contains(joined, "HEAD:refs/heads/master") {
				t.Fatalf("push args = %s", joined)
			}
		}
		if strings.Contains(joined, "config --global") || strings.Contains(joined, "--global user.") {
			t.Fatal("global git identity must never be modified")
		}
	}
	if !found {
		t.Fatal("dry-run push not executed")
	}
}

func TestProbeWritePermissionDenied(t *testing.T) {
	fake := execx.NewFake()
	fake.Handler = func(c execx.Call) execx.Result {
		if strings.HasPrefix(strings.Join(c.Args, " "), "push") {
			return execx.Result{ExitCode: 128, Stderr: "remote: Permission to celestezj/celestezj.github.io.git denied"}
		}
		return execx.Result{}
	}
	m := manager(fake)
	err := m.ProbeWrite(context.Background(), "git@alias:o/r.git", "master", "N", "e@x.com")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "AUTH_PERMISSION_INSUFFICIENT" {
		t.Fatalf("err = %v", err)
	}
}
