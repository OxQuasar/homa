package mainsite

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
)

// fakeSandbox tracks Ensure calls and captures the last Spec it saw.
// Allows asserting that the manager builds the right spec without going
// near a real podman.
type fakeSandbox struct {
	mu        sync.Mutex
	ensureN   int32
	stopN     int32
	lastSpec  sandbox.Spec
	ensureErr error
	stopErr   error
}

func (f *fakeSandbox) Ensure(_ context.Context, s sandbox.Spec) error {
	atomic.AddInt32(&f.ensureN, 1)
	f.mu.Lock()
	f.lastSpec = s
	f.mu.Unlock()
	return f.ensureErr
}
func (f *fakeSandbox) Stop(_ context.Context, _ string) error {
	atomic.AddInt32(&f.stopN, 1)
	return f.stopErr
}
func (f *fakeSandbox) IsRunning(_ context.Context, _ string) (bool, error) { return false, nil }
func (f *fakeSandbox) Logs(_ context.Context, _ string, _ int) ([]string, error) {
	return nil, nil
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func sampleCfg() Config {
	return Config{
		SiteTemplateDir: "/srv/homa/site-template",
		ImageRef:        "homa-sandbox:latest",
		HostPort:        40500,
		MemoryLimit:     "2g",
		CPULimit:        "2",
	}
}

// TestEnsureSpec — the spec passed to sandbox.Manager.Ensure carries the
// correct mainsite shape: HOMA_ROLE=main env, no NousPort, --no-rm,
// site-template bind mount.
func TestEnsureSpec(t *testing.T) {
	sb := &fakeSandbox{}
	mgr := New(sb, sampleCfg(), quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Cancel + stop so the watchdog goroutine doesn't outlive the test.
	cancel()
	mgr.Stop(context.Background())

	sb.mu.Lock()
	got := sb.lastSpec
	sb.mu.Unlock()

	if got.ContainerName != ContainerName {
		t.Errorf("ContainerName: got %q, want %q", got.ContainerName, ContainerName)
	}
	if got.NousPort != 0 {
		t.Errorf("NousPort: got %d, want 0 (mainsite has no nous to expose)", got.NousPort)
	}
	if got.PreviewPort != 40500 {
		t.Errorf("PreviewPort: got %d, want 40500", got.PreviewPort)
	}
	if got.WorktreePath != "/srv/homa/site-template" {
		t.Errorf("WorktreePath: got %q", got.WorktreePath)
	}
	if got.Env["HOMA_ROLE"] != "main" {
		t.Errorf("HOMA_ROLE env: got %q, want %q", got.Env["HOMA_ROLE"], "main")
	}
	if !got.NoAutoRemove {
		t.Error("NoAutoRemove: got false, want true (mainsite keeps crashed container for inspection)")
	}
	if got.ImageRef != "homa-sandbox:latest" {
		t.Errorf("ImageRef: got %q", got.ImageRef)
	}
}

// TestStartValidatesConfig — missing required fields fail fast at Start.
func TestStartValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty SiteTemplateDir", Config{ImageRef: "x", HostPort: 1}},
		{"empty ImageRef", Config{SiteTemplateDir: "/x", HostPort: 1}},
		{"zero HostPort", Config{SiteTemplateDir: "/x", ImageRef: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := New(&fakeSandbox{}, tc.cfg, quietLog())
			if err := mgr.Start(context.Background()); err == nil {
				t.Error("Start: got nil err, want validation failure")
			}
		})
	}
}

// TestEnsureFailureIsNonFatal — an initial Ensure error is logged but
// Start succeeds; the watchdog keeps trying. Mirrors the runtime behavior
// where a slow podman pull shouldn't crash the orchestrator.
func TestEnsureFailureIsNonFatal(t *testing.T) {
	sb := &fakeSandbox{ensureErr: errors.New("podman: pull timeout")}
	mgr := New(sb, sampleCfg(), quietLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("Start: got err %v, want nil (failure is logged, not propagated)", err)
	}
	if atomic.LoadInt32(&sb.ensureN) < 1 {
		t.Errorf("Ensure should have been called at least once at Start")
	}
	cancel()
	mgr.Stop(context.Background())
}

// TestStopIsIdempotent — Stop without Start is safe; Stop twice is safe.
func TestStopIsIdempotent(t *testing.T) {
	sb := &fakeSandbox{}
	mgr := New(sb, sampleCfg(), quietLog())
	// Pre-Start Stop must not panic.
	mgr.Stop(context.Background())

	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	mgr.Stop(context.Background())
	// Second Stop is also fine — stopFn is nil-guarded.
	mgr.Stop(context.Background())

	if atomic.LoadInt32(&sb.stopN) < 1 {
		t.Errorf("sandbox.Stop never called")
	}
}

// TestHostPort — getter mirrors config.
func TestHostPort(t *testing.T) {
	mgr := New(&fakeSandbox{}, Config{HostPort: 12345}, quietLog())
	if got := mgr.HostPort(); got != 12345 {
		t.Errorf("HostPort: got %d, want 12345", got)
	}
}

// Compile-time guarantee that watchdogInterval is sane (positive). Catches
// accidental const flips.
func TestWatchdogIntervalPositive(t *testing.T) {
	if watchdogInterval <= 0 {
		t.Fatalf("watchdogInterval must be > 0, got %v", watchdogInterval)
	}
	// Sanity: shorter than a hypothetical user-facing-timeout but long
	// enough not to hammer podman.
	if watchdogInterval > time.Minute {
		t.Errorf("watchdogInterval seems long: %v", watchdogInterval)
	}
}
