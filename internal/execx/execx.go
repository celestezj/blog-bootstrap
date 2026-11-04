// Package execx runs external commands through argv only, never a shell.
package execx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Result is the outcome of one external command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// Failed reports whether the command did not exit successfully.
func (r Result) Failed() bool { return r.Err != nil || r.ExitCode != 0 }

// Combined returns stdout followed by stderr.
func (r Result) Combined() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

// Runner abstracts command execution so tests can inject fakes.
type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) Result
	LookPath(name string) (string, error)
}

// Exec is the production Runner backed by os/exec.
type Exec struct{}

func (Exec) Run(ctx context.Context, dir string, env []string, name string, args ...string) Result {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			res.Err = nil
			if ctx.Err() != nil {
				res.Err = ctx.Err()
			}
			return res
		}
		res.ExitCode = -1
		return res
	}
	return res
}

func (Exec) LookPath(name string) (string, error) { return exec.LookPath(name) }

// FormatCommand renders a command for human-readable logs, quoting arguments.
func FormatCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			parts = append(parts, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}
