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

// TestEnsureEmitsMainflavorArgv — main-sandbox shape: NoAutoRemove=true
// (no --rm), NousPort=0 (no nous port mapping), HOMA_ROLE=main env. Catches
// regressions in buildRunArgs's flag-by-flag conditional emission.
func TestEnsureEmitsMainflavorArgv(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, args []string) ([]byte, error) {
			if len(args) > 0 && args[0] == "inspect" {
				return nil, runnertest.ExitError(name, 125, "no such container")
			}
			return []byte("container-id\n"), nil
		},
	}
	spec := sandbox.Spec{
		ContainerName: "homa-main",
		ImageRef:      "homa-sandbox:latest",
		WorktreePath:  "/srv/homa/site-template",
		NousPort:      0,
		PreviewPort:   40500,
		MemoryLimit:   "2g",
		CPULimit:      "2",
		Env:           map[string]string{"HOMA_ROLE": "main"},
		NoAutoRemove:  true,
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	if err := pm.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	runCall := fr.Calls()[1]
	wantArgs := []string{
		"run", "-d", "--replace", // no "--rm" (NoAutoRemove); --replace so a
		// stopped corpse of the same name doesn't block respawn.
		"--name", "homa-main",
		"-v", "/srv/homa/site-template:/workspace:Z",
		// no nous port mapping (NousPort=0)
		"-p", "127.0.0.1:40500:5173",
		"--memory=2g",
		"--cpus=2",
		"-e", "HOMA_ROLE=main",
		"homa-sandbox:latest",
	}
	if !reflect.DeepEqual(runCall.Args, wantArgs) {
		t.Errorf("main argv:\n got  %v\n want %v", runCall.Args, wantArgs)
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

// TestEnsureEmitsMountsAfterWorkspace — Spec.Mounts append `-v src:dst[:ro]`
// flags immediately after the workspace mount, before ports/limits, in
// slice order. ":ro" suffix is included iff Mount.ReadOnly is true.
func TestEnsureEmitsMountsAfterWorkspace(t *testing.T) {
	fr := &runnertest.FakeRunner{
		Responds: func(name string, args []string) ([]byte, error) {
			if len(args) > 0 && args[0] == "inspect" {
				return nil, runnertest.ExitError(name, 125, "no such container")
			}
			return []byte("container-id\n"), nil
		},
	}
	spec := sampleSpec()
	spec.Mounts = []sandbox.Mount{
		{Src: "/host/.claude/.credentials.json", Dst: "/root/.claude/.credentials.json", ReadOnly: true},
		{Src: "/host/data/extra", Dst: "/data/extra", ReadOnly: false},
	}
	pm := sandbox.NewPodmanManager("podman", fr)
	if err := pm.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	runCall := fr.Calls()[1]
	wantArgs := []string{
		"run", "-d", "--rm",
		"--name", "homa-user-abcd1234",
		"-v", "/var/homa/branches/abcd1234:/workspace:Z",
		"-v", "/host/.claude/.credentials.json:/root/.claude/.credentials.json:ro",
		"-v", "/host/data/extra:/data/extra",
		"-p", "127.0.0.1:40000:9000",
		"-p", "127.0.0.1:40001:5173",
		"--memory=2g",
		"--cpus=2",
		"-e", "ANTHROPIC_API_KEY=test-key",
		"homa-sandbox:latest",
	}
	if !reflect.DeepEqual(runCall.Args, wantArgs) {
		t.Errorf("run args:\n got  %v\n want %v", runCall.Args, wantArgs)
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
