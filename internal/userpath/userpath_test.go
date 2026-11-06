package userpath

import (
	"reflect"
	"testing"
)

func TestEnsurePrependsMissingDir(t *testing.T) {
	res := Ensure(`C:\Windows;C:\Tools`, `C:\root\.bootstrap\tools\node`, `C:\root\.bootstrap\tools\node\22\node-v22-win`)
	if !res.Changed {
		t.Fatal("expected a change")
	}
	if res.After != `C:\root\.bootstrap\tools\node\22\node-v22-win;C:\Windows;C:\Tools` {
		t.Fatalf("after = %q", res.After)
	}
	if !reflect.DeepEqual(res.Added, []string{`C:\root\.bootstrap\tools\node\22\node-v22-win`}) {
		t.Fatalf("added = %v", res.Added)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("removed = %v", res.Removed)
	}
}

func TestEnsureMovesExistingDirToFront(t *testing.T) {
	raw := `C:\Windows;C:\Tools;c:\root\.bootstrap\tools\node\22\node-v22-win`
	res := Ensure(raw, `C:\root\.bootstrap\tools\node`, `C:\root\.bootstrap\tools\node\22\node-v22-win`)
	if !res.Changed {
		t.Fatal("expected reordering")
	}
	if res.After != `C:\root\.bootstrap\tools\node\22\node-v22-win;C:\Windows;C:\Tools` {
		t.Fatalf("after = %q", res.After)
	}
	if len(res.Added) != 0 {
		t.Fatalf("added = %v, want none", res.Added)
	}
}

func TestEnsureRemovesStaleManagedEntries(t *testing.T) {
	root := `C:\root\.bootstrap\tools\node`
	old := root + `\22.14.0\node-v22.14.0-win-x64`
	cur := root + `\22.15.0\node-v22.15.0-win-x64`
	res := Ensure(`C:\Windows;`+old+`;C:\Other`, root, cur)
	if !res.Changed {
		t.Fatal("expected a change")
	}
	want := cur + `;C:\Windows;C:\Other`
	if res.After != want {
		t.Fatalf("after = %q", res.After)
	}
	if !reflect.DeepEqual(res.Removed, []string{old}) {
		t.Fatalf("removed = %v", res.Removed)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	cur := `C:\root\.bootstrap\tools\node\22.15.0\node-v22.15.0-win-x64`
	raw := cur + `;C:\Windows;`
	res := Ensure(raw, `C:\root\.bootstrap\tools\node`, cur)
	if res.Changed {
		t.Fatalf("expected no change, got %q", res.After)
	}
	if res.After != raw {
		t.Fatalf("after should stay raw, got %q", res.After)
	}
}

func TestEnsureEmptyPath(t *testing.T) {
	res := Ensure("", `C:\root\.bootstrap\tools\node`, `C:\root\.bootstrap\tools\node\22\node-v22-win`)
	if !res.Changed || res.After != `C:\root\.bootstrap\tools\node\22\node-v22-win` {
		t.Fatalf("result = %+v", res)
	}
}

func TestEnsurePreservesUnrelatedDuplicatesAndSeparators(t *testing.T) {
	res := Ensure(`C:\A;C:\B;C:\A`, `C:\root\.bootstrap\tools\node`, `C:\root\.bootstrap\tools\node\22`)
	want := `C:\root\.bootstrap\tools\node\22;C:\A;C:\B`
	if res.After != want {
		t.Fatalf("after = %q, want %q", res.After, want)
	}
}

func TestWithin(t *testing.T) {
	root := `C:\root\.bootstrap\tools\node`
	tests := []struct {
		path string
		want bool
	}{
		{root + `\22\node.exe`, true},
		{`c:\ROOT\.bootstrap\tools\node\22\node.exe`, true},
		{root, false},
		{`C:\root\.bootstrap\tools\node-other\x`, false},
		{`C:\Windows\node.exe`, false},
	}
	for _, tt := range tests {
		if got := Within(root, tt.path); got != tt.want {
			t.Errorf("Within(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestManagedReady(t *testing.T) {
	root := `C:\root\.bootstrap\tools\node`
	cur := root + `\22\node-v22-win`
	if !ManagedReady(cur+`;C:\Windows`, root, cur) {
		t.Fatal("expected ready")
	}
	if ManagedReady(`C:\Windows;`+cur, root, cur) {
		t.Fatal("desired dir must be first")
	}
	if ManagedReady(cur+`;`+root+`\21\old`, root, cur) {
		t.Fatal("stale managed entry must block readiness")
	}
}
