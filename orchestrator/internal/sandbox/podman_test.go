package sandbox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/sandbox/runnertest"
)

// sampleSpec builds a Spec for happy-path argv comparisons. Single source of
// truth so test changes don't drift across cases.
func sampleSpec() sandbox.Spec {
	return sandbox.Spec{
		ContainerName: "homa-user-abcd1234",
		ImageRef:      "homa-sandbox:latest",
		WorktreePath:  "/var/homa/branches/abcd1234",
		NousPort:      40000,
		PreviewPort:   40001,
		MemoryLimit:   "2g",
		CPULimit:      "2",
		Env:           map[string]string{"ANTHROPIC_API_KEY": "test-key"},
	}
}

// TestEnsureEmitsSpecArgv verifies the run argv matches mvp.md §8 verbatim.
func TestEnsureEmitsSpecArgv(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, args []string) ([]byte, error) {
			// inspect → absent (so Ensure proceeds to run)
			if len(args) > 0 && args[0] == "inspect" {
				return nil, runnertest.ExitError(name, 125, "no such container")
			}
			return []byte("container-id\n"), nil
		},
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	if err := pm.Ensure(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	calls := fr.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (inspect + run), got %d: %+v", len(calls), calls)
	}

	runCall := calls[1]
	wantArgs := []string{
		"run", "-d", "--rm",
		"--name", "homa-user-abcd1234",
		"-v", "/var/homa/branches/abcd1234:/workspace:Z",
		"-p", "127.0.0.1:40000:9000",
		"-p", "127.0.0.1:40001:5173",
		"--memory=2g",
		"--cpus=2",
		"-e", "ANTHROPIC_API_KEY=test-key",
		"homa-sandbox:latest",
	}
	if runCall.Name != "podman" {
		t.Errorf("run command: got %q, want %q", runCall.Name, "podman")
	}
	if !reflect.DeepEqual(runCall.Args, wantArgs) {
		t.Errorf("run args:\n got  %v\n want %v", runCall.Args, wantArgs)
	}
}

// TestEnsureSkipsIfRunning — when IsRunning returns true, no `run` invocation.
func TestEnsureSkipsIfRunning(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, args []string) ([]byte, error) {
			if len(args) > 0 && args[0] == "inspect" {
				return []byte("true\n"), nil
			}
			t.Fatalf("unexpected call: %s %v", name, args)
			return nil, nil
		},
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	if err := pm.Ensure(context.Background(), sampleSpec()); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, c := range fr.Calls() {
		if len(c.Args) > 0 && c.Args[0] == "run" {
			t.Fatalf("unexpected run call: %+v", c)
		}
	}
}

// TestIsRunningAbsentReturnsFalseNoErr — exit-status from `inspect` on a
// missing container surfaces as `(false, nil)`.
func TestIsRunningAbsentReturnsFalseNoErr(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, _ []string) ([]byte, error) {
			return nil, runnertest.ExitError(name, 125, "no such container")
		},
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	ok, err := pm.IsRunning(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("got true, want false")
	}
}

// TestIsRunningHardErrorBubbles — a non-exit error (e.g. binary missing)
// should NOT be silently converted to (false, nil).
func TestIsRunningHardErrorBubbles(t *testing.T) {
	want := errors.New("exec: \"podman\": executable file not found")
	fr := &runnertest.FakeRunner{
		Responds: func(_ string, _ []string) ([]byte, error) { return nil, want },
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	_, err := pm.IsRunning(context.Background(), "any")
	if err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("err: got %v, want wrap of %v", err, want)
	}
}

func TestIsRunningTrue(t *testing.T) {
	fr := &runnertest.FakeRunner{Responds: func(_ string, _ []string) ([]byte, error) { return []byte("true\n"), nil }}
	pm := sandbox.NewPodmanManager("podman", fr)
	got, _ := pm.IsRunning(context.Background(), "x")
	if !got {
		t.Error("got false, want true")
	}
}

func TestIsRunningFalse(t *testing.T) {
	fr := &runnertest.FakeRunner{Responds: func(_ string, _ []string) ([]byte, error) { return []byte("false\n"), nil }}
	pm := sandbox.NewPodmanManager("podman", fr)
	got, _ := pm.IsRunning(context.Background(), "x")
	if got {
		t.Error("got true, want false")
	}
}

// TestStopIdempotentOnAbsentContainer — `podman stop` on a missing container
// must return nil.
func TestStopIdempotentOnAbsentContainer(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, _ []string) ([]byte, error) {
			return nil, runnertest.ExitError(name, 125, "no such container")
		},
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	if err := pm.Stop(context.Background(), "ghost"); err != nil {
		t.Errorf("Stop on absent container: %v", err)
	}
}

// TestLogsParsesOutput — newline split, trailing newline trimmed.
func TestLogsParsesOutput(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(_ string, _ []string) ([]byte, error) {
			return []byte("line1\nline2\nline3\n"), nil
		},
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	lines, err := pm.Logs(context.Background(), "x", 60)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	want := []string{"line1", "line2", "line3"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("got %v, want %v", lines, want)
	}
}
