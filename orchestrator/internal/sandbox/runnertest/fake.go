// Package runnertest provides a shared FakeRunner used across sandbox /
// tsserve / worktree unit tests. Lives in its own package (not _test.go) so
// it's importable across the three packages without duplication.
package runnertest

import (
	"context"
	"sync"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
)

// CallRecord captures one invocation in the order it happened.
type CallRecord struct {
	Name string
	Args []string
}

// Responder decides what the fake should return for a given call. The fake
// records the call before calling Responder. Return ([]byte{}, nil) for the
// happy path; return a *sandbox.RunError to simulate non-zero exit.
type Responder func(name string, args []string) ([]byte, error)

// FakeRunner implements sandbox.Runner and records every call.
type FakeRunner struct {
	mu       sync.Mutex
	calls    []CallRecord
	Responds Responder
}

// Run records the call and dispatches to Responds (if set).
func (f *FakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	// Defensive copy of args so the recorded slice can't be mutated by the
	// caller after recording.
	cpy := make([]string, len(args))
	copy(cpy, args)
	f.calls = append(f.calls, CallRecord{Name: name, Args: cpy})
	f.mu.Unlock()
	if f.Responds == nil {
		return nil, nil
	}
	return f.Responds(name, args)
}

// Calls returns a snapshot of recorded calls.
func (f *FakeRunner) Calls() []CallRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]CallRecord, len(f.calls))
	copy(out, f.calls)
	return out
}

// ExitError builds a sandbox.RunError shaped like the real ExecRunner would
// return on non-zero exit. Convenience for Responder closures.
func ExitError(name string, code int, output string) error {
	return &sandbox.RunError{Cmd: name, ExitCode: code, Output: []byte(output)}
}
