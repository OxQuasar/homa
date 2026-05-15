package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// fakeManager records every Stop call so tests can assert the set/order.
type fakeManager struct {
	mu        sync.Mutex
	stopped   []string
	running   map[string]bool // name → running?
	stopErrs  map[string]error
}

func newFakeManager(running []string) *fakeManager {
	r := map[string]bool{}
	for _, n := range running {
		r[n] = true
	}
	return &fakeManager{running: r, stopErrs: map[string]error{}}
}

func (f *fakeManager) Ensure(_ context.Context, _ sandbox.Spec) error  { return nil }
func (f *fakeManager) Logs(_ context.Context, _ string, _ int) ([]string, error) {
	return nil, nil
}
func (f *fakeManager) IsRunning(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[name], nil
}
func (f *fakeManager) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.stopErrs[name]; ok {
		return err
	}
	f.stopped = append(f.stopped, name)
	f.running[name] = false
	return nil
}

func (f *fakeManager) stoppedSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.stopped))
	copy(out, f.stopped)
	sort.Strings(out) // order-independent comparison
	return out
}

// fakeLister returns a fixed slice of users.
type fakeLister struct{ users []store.UserSummary }

func (f *fakeLister) ListUsers(_ context.Context) ([]store.UserSummary, error) {
	return f.users, nil
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fixedNow returns a time function pinned to `now`.
func fixedNow(now int64) func() time.Time {
	return func() time.Time { return time.Unix(now, 0).UTC() }
}

// TestGCStopsIdleSandboxes — three users at {-45m, -60m, -5m}; idleAfter=30m
// → first two stopped, third skipped.
func TestGCStopsIdleSandboxes(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{
		{ID: "a", ContainerName: "homa-user-a", LastActiveAt: nowUnix - 45*60},
		{ID: "b", ContainerName: "homa-user-b", LastActiveAt: nowUnix - 60*60},
		{ID: "c", ContainerName: "homa-user-c", LastActiveAt: nowUnix - 5*60},
	}
	fm := newFakeManager([]string{"homa-user-a", "homa-user-b", "homa-user-c"})
	gc := New(fm, &fakeLister{users: users}, 30*time.Minute, time.Hour, discardLog())
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	want := []string{"homa-user-a", "homa-user-b"}
	if got := fm.stoppedSnapshot(); !equalStrings(got, want) {
		t.Errorf("stopped: got %v, want %v", got, want)
	}
}

// TestGCSkipsFreshSandboxes — every user inside idleAfter → no Stop calls.
func TestGCSkipsFreshSandboxes(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{
		{ID: "a", ContainerName: "homa-user-a", LastActiveAt: nowUnix - 5*60},
		{ID: "b", ContainerName: "homa-user-b", LastActiveAt: nowUnix - 10*60},
	}
	fm := newFakeManager([]string{"homa-user-a", "homa-user-b"})
	gc := New(fm, &fakeLister{users: users}, 30*time.Minute, time.Hour, discardLog())
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := fm.stoppedSnapshot(); len(got) != 0 {
		t.Errorf("stopped: got %v, want []", got)
	}
}

// TestGCToleratesStopError — Stop fails for one container; the next idle
// user in the same tick is still processed.
func TestGCToleratesStopError(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{
		{ID: "a", ContainerName: "homa-user-a", LastActiveAt: nowUnix - 45*60},
		{ID: "b", ContainerName: "homa-user-b", LastActiveAt: nowUnix - 50*60},
	}
	fm := newFakeManager([]string{"homa-user-a", "homa-user-b"})
	fm.stopErrs["homa-user-a"] = errors.New("podman: connection refused")
	gc := New(fm, &fakeLister{users: users}, 30*time.Minute, time.Hour, discardLog())
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// homa-user-a's Stop errored → not in `stopped` list. homa-user-b's
	// Stop succeeded.
	want := []string{"homa-user-b"}
	if got := fm.stoppedSnapshot(); !equalStrings(got, want) {
		t.Errorf("stopped: got %v, want %v", got, want)
	}
}

// TestGCSkipsStoppedContainers — container already not running → Stop NOT called.
func TestGCSkipsStoppedContainers(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{
		{ID: "a", ContainerName: "homa-user-a", LastActiveAt: nowUnix - 45*60},
	}
	fm := newFakeManager(nil) // nothing running
	gc := New(fm, &fakeLister{users: users}, 30*time.Minute, time.Hour, discardLog())
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := fm.stoppedSnapshot(); len(got) != 0 {
		t.Errorf("stopped: got %v, want []", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
