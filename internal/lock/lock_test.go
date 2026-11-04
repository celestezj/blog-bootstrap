package lock

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

func TestAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	l, err := Acquire(path, "run1", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("lock file should be removed")
	}
}

func TestConcurrentRunRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	l, err := Acquire(path, "run1", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	_, err = Acquire(path, "run2", time.Now)
	if err == nil {
		t.Fatal("second acquire should fail")
	}
	pe := protocol.As(err)
	if pe == nil || pe.Code != "LOCK_HELD" {
		t.Fatalf("error = %v, want LOCK_HELD", err)
	}
	if pe.Class != protocol.ClassRetry {
		t.Fatalf("class = %s, want retry", pe.Class)
	}
}

func TestConcurrentStaleTakeoverSingleWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	host, _ := os.Hostname()
	stale := Info{PID: 1 << 30, Hostname: host, StartedAt: time.Now().Add(-time.Hour), RunID: "dead"}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	start := make(chan struct{})
	results := make(chan *FileLock, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if l, err := Acquire(path, fmt.Sprintf("run-%d", i), time.Now); err == nil {
				results <- l
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	var locks []*FileLock
	for l := range results {
		locks = append(locks, l)
	}
	if len(locks) != 1 {
		for _, l := range locks {
			_ = l.Release()
		}
		t.Fatalf("successful acquires = %d, want exactly 1", len(locks))
	}
	if err := locks[0].Release(); err != nil {
		t.Fatal(err)
	}
}

func TestStaleLockTakenOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	host, _ := os.Hostname()
	stale := Info{PID: 1 << 30, Hostname: host, StartedAt: time.Now().Add(-time.Hour), RunID: "dead"}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(path, "run2", time.Now)
	if err != nil {
		t.Fatalf("stale lock should be taken over: %v", err)
	}
	defer l.Release()
	if l.Info().RunID != "run2" {
		t.Fatal("lock not replaced")
	}
}
