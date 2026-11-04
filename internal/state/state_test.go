package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestSetStepPersistsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bootstrap", "state.json")
	s := New(path, fixedNow)
	if err := s.SetStep("auth.ssh", StepState{Status: StatusVerified, UpdatedAt: fixedNow(), Evidence: map[string]string{"a": "b"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Open(path, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Snapshot().Steps["auth.ssh"].Status; got != StatusVerified {
		t.Fatalf("status = %q", got)
	}
}

func TestSetStepClearsReceipt(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "state.json"), fixedNow)
	if err := s.WriteReceipt(&Receipt{PlanHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Receipt == nil {
		t.Fatal("receipt missing")
	}
	if err := s.SetStep("node", StepState{Status: StatusVerified}); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Receipt != nil {
		t.Fatal("receipt must be cleared when state changes")
	}
}

func TestFailStepInvalidatesDownstream(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "state.json"), fixedNow)
	order := []string{"auth.ssh", "node", "workspace", "preview"}
	for _, id := range order {
		if err := s.SetStep(id, StepState{Status: StatusVerified}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FailStep(order, "node", StepState{Status: StatusFailed, ErrorCode: "X"}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.Steps["auth.ssh"].Status != StatusVerified {
		t.Error("upstream step must stay verified")
	}
	if snap.Steps["node"].Status != StatusFailed {
		t.Error("failing step must be failed")
	}
	for _, id := range []string{"workspace", "preview"} {
		if snap.Steps[id].Status != StatusInvalid {
			t.Errorf("downstream %s = %q, want invalid", id, snap.Steps[id].Status)
		}
	}
}

func TestOpenCorruptStateFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, fixedNow); err == nil {
		t.Fatal("expected corrupt state to fail rather than be silently reset")
	}
}
