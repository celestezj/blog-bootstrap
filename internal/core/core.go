// Package core holds the shared runtime context passed to every step.
package core

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/celestezj/blog-bootstrap/internal/config"
	"github.com/celestezj/blog-bootstrap/internal/execx"
	"github.com/celestezj/blog-bootstrap/internal/state"
)

// Browser opens a URL in the user's browser via argv, never a shell.
type Browser interface {
	Open(url string) error
}

// BrowserFunc adapts a function to the Browser interface.
type BrowserFunc func(url string) error

func (f BrowserFunc) Open(url string) error { return f(url) }

// Context carries configuration, environment and injected dependencies.
type Context struct {
	Root         string
	Home         string
	ConfigPath   string
	Config       *config.Resolved
	ConfigDigest string
	State        *state.Store
	Runner       execx.Runner
	HTTP         *http.Client
	Browser      Browser
	Logger       *Logger

	GOOS   string
	GOARCH string
	IsTTY  bool

	Now   func() time.Time
	Sleep func(time.Duration)

	Stdout io.Writer
	Stderr io.Writer

	// Progress receives human-facing progress lines. nil disables output.
	Progress func(string)

	// PlanHash is set for run and recorded in the receipt.
	PlanHash string
}

// Sayf emits one human-facing progress line when progress output is enabled.
func (c *Context) Sayf(format string, args ...interface{}) {
	if c.Progress == nil {
		return
	}
	c.Progress(fmt.Sprintf(format, args...))
}

// BootstrapDir is the installer's private working directory.
func (c *Context) BootstrapDir() string { return filepath.Join(c.Root, ".bootstrap") }

// ToolsDir holds portable toolchains.
func (c *Context) ToolsDir() string { return filepath.Join(c.BootstrapDir(), "tools") }

// LogDir holds per-step logs.
func (c *Context) LogDir() string { return filepath.Join(c.BootstrapDir(), "logs") }

// StatePath is the cache/state document.
func (c *Context) StatePath() string { return filepath.Join(c.BootstrapDir(), "state.json") }

// ChangesPath is the rollback record.
func (c *Context) ChangesPath() string { return filepath.Join(c.BootstrapDir(), "changes.json") }

// NowTime returns the injectable clock value.
func (c *Context) NowTime() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Wait sleeps with the injectable sleeper (tests record instead of sleeping).
func (c *Context) Wait(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Logger appends per-step output and keeps a bounded tail for error reports.
type Logger struct {
	dir     string
	maxTail int
	mu      sync.Mutex
}

// NewLogger creates a logger rooted at dir with an 8 KiB tail.
func NewLogger(dir string) *Logger { return &Logger{dir: dir, maxTail: 8192} }

func (l *Logger) Path(step string) string {
	return filepath.Join(l.dir, safeName(step)+".log")
}

// Begin truncates the step's log.
func (l *Logger) Begin(step string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.Path(step), nil, 0o600)
}

// Append writes redacted output to the step log and returns its path.
func (l *Logger) Append(step, label, text string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	path := l.Path(step)
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return path
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return path
	}
	defer f.Close()
	header := ""
	if label != "" {
		header = "== " + label + " ==\n"
	}
	_, _ = f.WriteString(header + redact(text) + "\n")
	return path
}

// Tail returns the last maxTail bytes of the step log.
func (l *Logger) Tail(step string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.Path(step))
	if err != nil {
		return ""
	}
	if len(data) > l.maxTail {
		data = data[len(data)-l.maxTail:]
	}
	return string(data)
}

func safeName(id string) string {
	return strings.NewReplacer(".", "-", "/", "-", "\\", "-").Replace(id)
}

// redact removes any private key block that a child process might print.
func redact(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	skipping := false
	for _, line := range lines {
		if strings.Contains(line, "PRIVATE KEY-----") {
			if strings.Contains(line, "BEGIN") {
				skipping = true
				out = append(out, "[redacted private key]")
				continue
			}
			skipping = false
			continue
		}
		if skipping {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
