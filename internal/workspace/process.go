package workspace

import (
	"context"
	"os/exec"
	"sync"
)

// ProcessSpec describes a long-running child process.
type ProcessSpec struct {
	Dir  string
	Env  []string
	Name string
	Args []string
}

// Process is a started child with controllable process-tree termination.
type Process interface {
	KillTree() error
	Wait() error
	Output() string
	Done() <-chan struct{}
}

// Starter starts a child process. Tests inject a fake.
type Starter interface {
	Start(ctx context.Context, spec ProcessSpec) (Process, error)
}

// ExecStarter is the production starter backed by os/exec.
type ExecStarter struct{}

func (ExecStarter) Start(ctx context.Context, spec ProcessSpec) (Process, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	buf := &lockedBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tree, _ := attachTree(cmd)
	p := &execProcess{cmd: cmd, buf: buf, done: make(chan struct{}), tree: tree}
	go func() {
		p.waitErr = cmd.Wait()
		if tree != nil {
			_ = tree.Close()
		}
		close(p.done)
	}()
	return p, nil
}

// treeHandle owns a platform process-tree primitive. Closing it terminates
// every member; closing an already-empty tree is harmless.
type treeHandle interface {
	Close() error
}

type execProcess struct {
	cmd     *exec.Cmd
	buf     *lockedBuffer
	done    chan struct{}
	tree    treeHandle
	waitErr error
}

func (p *execProcess) Done() <-chan struct{} { return p.done }

func (p *execProcess) Wait() error {
	<-p.done
	return p.waitErr
}

func (p *execProcess) Output() string { return p.buf.String() }

// KillTree terminates the whole process tree. A process that already exited is
// not an error.
func (p *execProcess) KillTree() error {
	select {
	case <-p.done:
		return nil
	default:
	}
	if p.tree != nil {
		err := p.tree.Close()
		<-p.done
		return err
	}
	err := killProcessTree(p.cmd)
	<-p.done
	return err
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
