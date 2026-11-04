package execx

import (
	"context"
	"sync"
)

// Call records one fake invocation.
type Call struct {
	Dir  string
	Env  []string
	Name string
	Args []string
}

// Fake is a scripted Runner for tests and isolated acceptance runs.
type Fake struct {
	mu        sync.Mutex
	Calls     []Call
	LookPaths map[string]string
	Handler   func(call Call) Result
}

// NewFake builds an empty fake runner.
func NewFake() *Fake { return &Fake{LookPaths: map[string]string{}} }

func (f *Fake) Run(ctx context.Context, dir string, env []string, name string, args ...string) Result {
	f.mu.Lock()
	call := Call{Dir: dir, Env: env, Name: name, Args: append([]string(nil), args...)}
	f.Calls = append(f.Calls, call)
	handler := f.Handler
	f.mu.Unlock()
	if handler != nil {
		return handler(call)
	}
	return Result{}
}

func (f *Fake) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.LookPaths[name]
	if !ok {
		return "", errNotFound(name)
	}
	return p, nil
}

// CallsMatching returns recorded calls whose executable base matches name.
func (f *Fake) CallsMatching(name string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.Calls {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

type notFoundError string

func (e notFoundError) Error() string { return "executable " + string(e) + " not found" }

func errNotFound(name string) error { return notFoundError(name) }
