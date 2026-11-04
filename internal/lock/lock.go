// Package lock implements a PID-bearing single-instance lock with safe
// takeover after a crash.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/protocol"
)

// Info is the on-disk lock document.
type Info struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
	RunID     string    `json:"run_id"`
}

// FileLock is an acquired lock.
type FileLock struct {
	path string
	info Info
}

// Acquire takes the lock, refusing when a live process on this host holds it.
// A lock left by a dead process is taken over.
func Acquire(path, runID string, now func() time.Time) (*FileLock, error) {
	if now == nil {
		now = time.Now
	}
	host, _ := os.Hostname()
	info := Info{PID: os.Getpid(), Hostname: host, StartedAt: now(), RunID: runID}

	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		err := createExclusive(path, info)
		if err == nil {
			return &FileLock{path: path, info: info}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		existing, readErr := read(path)
		if readErr == nil {
			if existing.Hostname == host && processAlive(existing.PID) {
				return nil, protocol.Retry("LOCK_HELD",
					fmt.Sprintf("另一个安装实例正在运行（PID %d，开始于 %s）", existing.PID, existing.StartedAt.Format(time.RFC3339))).
					WithDetails(map[string]string{"pid": strconv.Itoa(existing.PID), "run_id": existing.RunID})
			}
			if existing.Hostname != host {
				return nil, protocol.Retry("LOCK_HELD",
					fmt.Sprintf("锁文件来自主机 %s（PID %d），无法确认是否仍在运行", existing.Hostname, existing.PID)).
					WithDetails(map[string]string{"pid": strconv.Itoa(existing.PID), "hostname": existing.Hostname})
			}
		}
		if readErr != nil {
			// A fresh but incomplete file is another process mid-create, not a
			// stale lock. Only corrupt locks older than the grace period are
			// taken over.
			if st, statErr := os.Stat(path); statErr == nil && now().Sub(st.ModTime()) < 2*time.Second {
				return nil, protocol.Retry("LOCK_HELD", "锁文件正在被其他进程创建，请稍后重试")
			}
		}
		// Stale or unreadable lock: atomically claim the file by renaming it
		// away, then retry the exclusive create. Only one process can win the
		// rename, so concurrent takeovers cannot both proceed.
		claimed := fmt.Sprintf("%s.stale.%d.%d.%d", path, os.Getpid(), now().UnixNano(), attempt)
		if renameErr := os.Rename(path, claimed); renameErr != nil {
			if errors.Is(renameErr, os.ErrNotExist) {
				continue
			}
			return nil, protocol.Retry("LOCK_HELD", "无法接管锁文件: "+renameErr.Error()).
				WithCause(renameErr)
		}
		_ = os.Remove(claimed)
	}
	return nil, protocol.Retry("LOCK_HELD", "无法获取锁：锁文件正在被其他进程接管")
}

// Release removes the lock only when it still belongs to this run.
func (l *FileLock) Release() error {
	if l == nil {
		return nil
	}
	cur, err := read(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if cur.RunID != l.info.RunID || cur.PID != l.info.PID {
		return nil
	}
	return os.Remove(l.path)
}

// Info returns the lock holder information.
func (l *FileLock) Info() Info { return l.info }

func createExclusive(path string, info Info) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func read(path string) (Info, error) {
	var info Info
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	return info, nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
