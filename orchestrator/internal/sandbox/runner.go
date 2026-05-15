// Package sandbox manages per-user Podman containers and provides the shared
// `Runner` seam used by sandbox + tsserve + worktree to invoke host binaries
// (podman, tailscale, git). Tests inject a fake Runner; production wires the
// `ExecRunner` that wraps os/exec.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner runs a single command and returns its combined stdout/stderr.
//
// An error of type *RunError means the command ran but exited non-zero —
// typically tolerable (e.g. `podman inspect` on an absent container). Any
// other error (binary missing, context canceled) is a hard failure.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// RunError is returned by Runner implementations when the command executed
// but its process exited with a non-zero code. Callers use errors.As to
// distinguish this from "couldn't start the binary at all".
type RunError struct {
	Cmd      string
	ExitCode int
	Output   []byte
}

func (e *RunError) Error() string {
	return fmt.Sprintf("%s exit %d: %s", e.Cmd, e.ExitCode, string(e.Output))
}

// IsExitError reports whether err is a *RunError (i.e. command ran & failed,
// not "couldn't even start"). Convenience helper for callers that only need
// the boolean.
func IsExitError(err error) bool {
	var re *RunError
	return errors.As(err, &re)
}

// ExecRunner is the production Runner: wraps exec.CommandContext + CombinedOutput.
type ExecRunner struct{}

// Run executes name with args via exec.CommandContext. Returns combined
// stdout/stderr. Non-zero exit is surfaced as *RunError.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err == nil {
		return out, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, &RunError{Cmd: name, ExitCode: ee.ExitCode(), Output: out}
	}
	return out, err
}
