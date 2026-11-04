package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	procRoleEnv = "BLOG_BOOTSTRAP_TEST_ROLE"
	beatEnv     = "BLOG_BOOTSTRAP_TEST_BEAT"
)

func roleEnv(role, beat string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, procRoleEnv+"=") || strings.HasPrefix(kv, beatEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, procRoleEnv+"="+role, beatEnv+"="+beat)
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.Size()
}

// TestKillTreeStopsDescendants starts a parent process that in turn starts a
// grandchild writing a heartbeat, then proves killing the parent's tree stops
// the grandchild on both Windows (taskkill /T) and POSIX (process group).
func TestKillTreeStopsDescendants(t *testing.T) {
	switch os.Getenv(procRoleEnv) {
	case "parent":
		beat := os.Getenv(beatEnv)
		cmd := exec.Command(os.Args[0], "-test.run=^TestKillTreeStopsDescendants$")
		cmd.Env = roleEnv("grandchild", beat)
		if err := cmd.Start(); err != nil {
			os.Exit(3)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "grandchild":
		beat := os.Getenv(beatEnv)
		f, err := os.OpenFile(beat, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(4)
		}
		defer f.Close()
		for {
			if _, err := f.WriteString("x"); err != nil {
				return
			}
			_ = f.Sync()
			time.Sleep(50 * time.Millisecond)
		}
	}

	beat := filepath.Join(t.TempDir(), "heartbeat")
	proc, err := (ExecStarter{}).Start(context.Background(), ProcessSpec{
		Name: os.Args[0],
		Args: []string{"-test.run=^TestKillTreeStopsDescendants$"},
		Env:  roleEnv("parent", beat),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for fileSize(beat) <= 0 {
		if time.Now().After(deadline) {
			out := proc.Output()
			_ = proc.KillTree()
			t.Fatalf("helper process did not write its heartbeat: %s", out)
		}
		time.Sleep(25 * time.Millisecond)
	}

	if err := proc.KillTree(); err != nil {
		t.Fatalf("kill tree: %v", err)
	}
	_ = proc.Wait()

	time.Sleep(250 * time.Millisecond)
	before := fileSize(beat)
	time.Sleep(300 * time.Millisecond)
	after := fileSize(beat)
	if before != after {
		t.Fatalf("descendant still running after tree kill: %d -> %d bytes", before, after)
	}
}
