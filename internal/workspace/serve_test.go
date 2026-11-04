package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

type fakeProcess struct {
	output string
	done   chan struct{}
	killed bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{done: make(chan struct{})}
}

func (p *fakeProcess) KillTree() error {
	select {
	case <-p.done:
	default:
		p.killed = true
		close(p.done)
	}
	return nil
}

func (p *fakeProcess) Wait() error { <-p.done; return nil }

func (p *fakeProcess) Output() string { return p.output }

func (p *fakeProcess) Done() <-chan struct{} { return p.done }

type fakeStarter struct {
	procs []*fakeProcess
	errs  []error
	calls []ProcessSpec
}

func (s *fakeStarter) Start(_ context.Context, spec ProcessSpec) (Process, error) {
	s.calls = append(s.calls, spec)
	i := len(s.calls) - 1
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	if err != nil {
		return nil, err
	}
	if i < len(s.procs) {
		return s.procs[i], nil
	}
	return newFakeProcess(), nil
}

type fakePicker struct {
	ports []int
	i     int
}

func (p *fakePicker) Pick() (int, error) {
	port := p.ports[p.i]
	p.i++
	return port, nil
}

func fastWait(time.Duration) {}

func TestSmokeSuccessKillsTree(t *testing.T) {
	proc := newFakeProcess()
	starter := &fakeStarter{procs: []*fakeProcess{proc}}
	picker := &fakePicker{ports: []int{4001}}
	server := &Server{
		Starter:  starter,
		Ports:    picker,
		Probe:    func(context.Context, string) (int, error) { return 200, nil },
		Wait:     fastWait,
		Interval: time.Millisecond,
		MaxPolls: 3,
	}
	evidence, err := server.Smoke(context.Background(), t.TempDir(), "node", "hexo.js")
	if err != nil {
		t.Fatal(err)
	}
	if !proc.killed {
		t.Fatal("process tree must be killed after verification")
	}
	if evidence["port"] != "4001" || evidence["http_status"] != "200" {
		t.Fatalf("evidence = %v", evidence)
	}
	args := strings.Join(starter.calls[0].Args, " ")
	if !strings.Contains(args, "server") || strings.Contains(args, "preview") {
		t.Fatalf("args = %s", args)
	}
	if starter.calls[0].Name != "node" {
		t.Fatalf("name = %s", starter.calls[0].Name)
	}
}

func TestSmokeRetriesOnAnotherPort(t *testing.T) {
	failed := newFakeProcess()
	ok := newFakeProcess()
	starter := &fakeStarter{
		procs: []*fakeProcess{failed, ok},
		errs:  []error{errPortBusy{}},
	}
	picker := &fakePicker{ports: []int{4001, 4002}}
	server := &Server{
		Starter:  starter,
		Ports:    picker,
		Probe:    func(context.Context, string) (int, error) { return 200, nil },
		Wait:     fastWait,
		MaxPolls: 2,
	}
	evidence, err := server.Smoke(context.Background(), t.TempDir(), "node", "hexo.js")
	if err != nil {
		t.Fatal(err)
	}
	if evidence["port"] != "4002" || evidence["attempt"] != "2" {
		t.Fatalf("evidence = %v", evidence)
	}
	if len(starter.calls) != 2 {
		t.Fatalf("starts = %d", len(starter.calls))
	}
}

type errPortBusy struct{}

func (errPortBusy) Error() string { return "port already in use" }

func TestSmokeTimeoutKillsTree(t *testing.T) {
	proc := newFakeProcess()
	starter := &fakeStarter{procs: []*fakeProcess{proc}}
	picker := &fakePicker{ports: []int{4001}}
	server := &Server{
		Starter:  starter,
		Ports:    picker,
		Probe:    func(context.Context, string) (int, error) { return 500, nil },
		Wait:     fastWait,
		MaxPolls: 2,
		Attempts: 1,
	}
	_, err := server.Smoke(context.Background(), t.TempDir(), "node", "hexo.js")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "PREVIEW_TIMEOUT" {
		t.Fatalf("err = %v", err)
	}
	if !proc.killed {
		t.Fatal("process tree must be killed after timeout")
	}
}

func TestSmokeDetectsEarlyExit(t *testing.T) {
	proc := newFakeProcess()
	proc.output = "listen EADDRINUSE 127.0.0.1:4001"
	close(proc.done)
	starter := &fakeStarter{procs: []*fakeProcess{proc}}
	picker := &fakePicker{ports: []int{4001}}
	server := &Server{
		Starter:  starter,
		Ports:    picker,
		Probe:    func(context.Context, string) (int, error) { t.Fatal("probe should not run"); return 0, nil },
		Wait:     fastWait,
		Attempts: 1,
	}
	_, err := server.Smoke(context.Background(), t.TempDir(), "node", "hexo.js")
	pe := protocol.As(err)
	if pe == nil || pe.Code != "PREVIEW_FAILED" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(pe.Details["output"], "EADDRINUSE") {
		t.Fatalf("details = %v", pe.Details)
	}
}
