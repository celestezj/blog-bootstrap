// Package lock implements a PID-bearing single-instance lock with safe
// takeover after a crash. Ownership is enforced by an OS file lock, which the
// kernel releases when the process dies; the file contents are informational.
package lock

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
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
	file *os.File
}

// Acquire takes the lock, refusing when another process holds it. A lock left
// by a dead process is taken over, because the kernel drops its file lock.
func Acquire(path, runID string, now func() time.Time) (*FileLock, error) {
	if now == nil {
		now = time.Now
	}
	host, _ := os.Hostname()
	info := Info{PID: os.Getpid(), Hostname: host, StartedAt: now(), RunID: runID}

	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLock(f); err != nil {
		existing, _ := readInfo(f)
		_ = f.Close()
		msg := "另一个安装实例正在运行"
		details := map[string]string{}
		if existing.RunID != "" {
			msg = fmt.Sprintf("另一个安装实例正在运行（PID %d，开始于 %s）",
				existing.PID, existing.StartedAt.Format(time.RFC3339))
			details["pid"] = strconv.Itoa(existing.PID)
			details["run_id"] = existing.RunID
			if existing.Hostname != "" {
				details["hostname"] = existing.Hostname
			}
		}
		pe := protocol.Retry("LOCK_HELD", msg)
		if len(details) > 0 {
			pe.WithDetails(details)
		}
		return nil, pe
	}
	if err := writeInfo(f, info); err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		return nil, err
	}
	return &FileLock{path: path, info: info, file: f}, nil
}

// Release removes the lock when it is still held by this handle.
func (l *FileLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	// On Unix, unlink while still holding the lock: nothing else can acquire
	// the old inode before it disappears.
	if runtime.GOOS != "windows" {
		_ = os.Remove(l.path)
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	// On Windows the file must be closed before it can be deleted; failure
	// means another process already holds or opened it, which is fine.
	if runtime.GOOS == "windows" {
		_ = os.Remove(l.path)
	}
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// Info returns the lock holder information.
func (l *FileLock) Info() Info { return l.info }

func readInfo(f *os.File) (Info, error) {
	var info Info
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return info, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	return info, nil
}

func writeInfo(f *os.File, info Info) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
