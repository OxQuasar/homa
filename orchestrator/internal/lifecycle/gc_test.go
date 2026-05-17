package lifecycle

import (
	"context"
	"encoding/json"
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

func (f *fakeManager) Ensure(_ context.Context, _ sandbox.Spec) error { return nil }
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
	sort.Strings(out)
	return out
}

// fakeLister returns a fixed slice of users.
type fakeLister struct{ users []store.UserSummary }

func (f *fakeLister) ListUsers(_ context.Context) ([]store.UserSummary, error) {
	return f.users, nil
}

// fakeHub records Disconnect and SendToUser calls so tests can assert
// warning emission + disconnect-before-compact order.
type fakeHub struct {
	mu          sync.Mutex
	disconnects []string                // userIDs disconnected
	sends       map[string][]json.RawMessage // userID → frames sent
}

func newFakeHub() *fakeHub {
	return &fakeHub{sends: map[string][]json.RawMessage{}}
}
func (h *fakeHub) Disconnect(userID, _ string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disconnects = append(h.disconnects, userID)
	return 1
}
func (h *fakeHub) SendToUser(userID string, raw []byte) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]byte, len(raw))
	copy(cp, raw)
	h.sends[userID] = append(h.sends[userID], cp)
	return 1
}

// fakeCompactor records compaction calls; configurable return value
// (promptTokens) + error. Defaults: promptTokens=0, err=nil → success.
type fakeCompactor struct {
	mu                sync.Mutex
	calls             []string // sessionIDs the compactor was asked to run on
	minTokens         []int64  // minTokens passed each call
	promptTokensReply int64    // return value
	err               error
}

func (c *fakeCompactor) Run(_ context.Context, port int, sessionID, _ string, minTokens int64, _ time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, sessionID)
	c.minTokens = append(c.minTokens, minTokens)
	_ = port
	return c.promptTokensReply, c.err
}
func (c *fakeCompactor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func fixedNow(now int64) func() time.Time {
	return func() time.Time { return time.Unix(now, 0).UTC() }
}

// sampleCfg — 60m idle, 60s warn window, 90s compact timeout, 50k token
// gate. Pick interval long enough that Tick() is the only thing driving
// state changes in tests.
func sampleCfg() Config {
	return Config{
		IdleAfter:        60 * time.Minute,
		WarningWindow:    60 * time.Second,
		CompactTimeout:   90 * time.Second,
		CompactMinTokens: 50_000,
		Interval:         time.Hour,
	}
}

func buildGC(t *testing.T, users []store.UserSummary, running []string, hub Hub, c Compactor) (*GC, *fakeManager) {
	t.Helper()
	fm := newFakeManager(running)
	gc := New(fm, &fakeLister{users: users}, hub, c, sampleCfg(), discardLog())
	return gc, fm
}

// TestCompactThenStopBeyondIdleAfter — user idle > IdleAfter triggers the
// full sequence: hub.Disconnect → compactor.Run → sandbox.Stop. Asserts
// all three fire and in that order via deterministic call counts.
func TestCompactThenStopBeyondIdleAfter(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		NousPort: 40000, NousSessionID: "sess-a", WorktreePath: "/work/a",
		LastMessageAt: nowUnix - 70*60, // 70 min ago → past 60m IdleAfter
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{}
	gc, fm := buildGC(t, users, []string{"homa-user-a"}, hub, cm)
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got := equalStrings(hub.disconnects, []string{"a"}); !got {
		t.Errorf("hub.Disconnect calls: got %v, want [a]", hub.disconnects)
	}
	if cm.callCount() != 1 {
		t.Errorf("compactor calls: got %d, want 1", cm.callCount())
	}
	if got := fm.stoppedSnapshot(); !equalStrings(got, []string{"homa-user-a"}) {
		t.Errorf("stopped: got %v, want [homa-user-a]", got)
	}
}

// TestWarningFrameInLastMinute — user idle within WarningWindow of IdleAfter
// → hub.SendToUser invoked with a homa.idle_warning frame, no compact/stop.
func TestWarningFrameInLastMinute(t *testing.T) {
	const nowUnix = 1_700_000_000
	// 59min30s ago → within 60s warning window of 60min IdleAfter.
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		LastMessageAt: nowUnix - (59*60 + 30),
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{}
	gc, fm := buildGC(t, users, []string{"homa-user-a"}, hub, cm)
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	frames := hub.sends["a"]
	if len(frames) != 1 {
		t.Fatalf("warning frames sent: got %d, want 1", len(frames))
	}
	var probe map[string]any
	if err := json.Unmarshal(frames[0], &probe); err != nil {
		t.Fatalf("warning frame not JSON: %v", err)
	}
	if probe["type"] != "homa.idle_warning" {
		t.Errorf("frame type: got %v, want homa.idle_warning", probe["type"])
	}
	if _, ok := probe["seconds_until_compact"]; !ok {
		t.Error("frame missing seconds_until_compact")
	}
	// Compact / stop must NOT fire during the warning window.
	if cm.callCount() != 0 {
		t.Errorf("compactor fired during warning window: %d calls", cm.callCount())
	}
	if got := fm.stoppedSnapshot(); len(got) != 0 {
		t.Errorf("stop fired during warning window: %v", got)
	}
	if len(hub.disconnects) != 0 {
		t.Errorf("disconnect fired during warning window: %v", hub.disconnects)
	}
}

// TestNoActionForFreshUser — user idle < (IdleAfter - WarningWindow):
// no disconnect, no compact, no stop, no warning frame.
func TestNoActionForFreshUser(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		LastMessageAt: nowUnix - 5*60, // 5 min idle
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{}
	gc, fm := buildGC(t, users, []string{"homa-user-a"}, hub, cm)
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hub.disconnects) != 0 || len(hub.sends) != 0 ||
		cm.callCount() != 0 || len(fm.stoppedSnapshot()) != 0 {
		t.Errorf("fresh user provoked action: dc=%v sends=%v compact=%d stop=%v",
			hub.disconnects, hub.sends, cm.callCount(), fm.stoppedSnapshot())
	}
}

// TestCompactMinTokensPropagated — the CompactMinTokens config field
// reaches the compactor unchanged. Sanity that config wiring isn't dropped.
func TestCompactMinTokensPropagated(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		NousPort: 40000, NousSessionID: "sess-a", WorktreePath: "/work/a",
		LastMessageAt: nowUnix - 70*60,
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{}
	gc, _ := buildGC(t, users, []string{"homa-user-a"}, hub, cm)
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(cm.minTokens) != 1 {
		t.Fatalf("compactor calls: got %d, want 1", len(cm.minTokens))
	}
	if cm.minTokens[0] != 50_000 {
		t.Errorf("minTokens passed: got %d, want 50000 (sampleCfg default)", cm.minTokens[0])
	}
}

// TestStopProceedsWhenCompactSkipsBelowThreshold — compactor returns
// ErrBelowThreshold → lifecycle logs at info, Stop still fires. The
// behavioral observable from the test side is the same as a successful
// compact: Stop ran. But internally this is the "no LLM call wasted"
// path.
func TestStopProceedsWhenCompactSkipsBelowThreshold(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		NousPort: 40000, NousSessionID: "sess-a", WorktreePath: "/work/a",
		LastMessageAt: nowUnix - 70*60,
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{err: ErrBelowThreshold}
	gc, fm := buildGC(t, users, []string{"homa-user-a"}, hub, cm)
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := fm.stoppedSnapshot(); !equalStrings(got, []string{"homa-user-a"}) {
		t.Errorf("stop did not proceed after below-threshold skip: %v", got)
	}
}

// TestStopProceedsWhenCompactFails — compactor.Run errors → Stop still
// fires (best-effort: compaction failure shouldn't pin a container open).
func TestStopProceedsWhenCompactFails(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		NousPort: 40000, NousSessionID: "sess-a", WorktreePath: "/work/a",
		LastMessageAt: nowUnix - 70*60,
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{err: errors.New("session busy")}
	gc, fm := buildGC(t, users, []string{"homa-user-a"}, hub, cm)
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if cm.callCount() != 1 {
		t.Errorf("compactor not called: %d", cm.callCount())
	}
	if got := fm.stoppedSnapshot(); !equalStrings(got, []string{"homa-user-a"}) {
		t.Errorf("stop did not proceed after compact failure: %v", got)
	}
}

// TestStoppedContainerSkipped — IsRunning=false → no Stop/compact even if
// idle. Avoids redundant work when GC respawned-and-removed concurrently.
func TestStoppedContainerSkipped(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{{
		ID: "a", ContainerName: "homa-user-a",
		LastMessageAt: nowUnix - 70*60,
	}}
	hub := newFakeHub()
	cm := &fakeCompactor{}
	gc, fm := buildGC(t, users, nil, hub, cm) // running: empty
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if cm.callCount() != 0 || len(fm.stoppedSnapshot()) != 0 || len(hub.disconnects) != 0 {
		t.Errorf("acted on stopped container: dc=%v compact=%d stop=%v",
			hub.disconnects, cm.callCount(), fm.stoppedSnapshot())
	}
}

// TestStopToleratesError — Stop fails for one user; other idle users in
// the same tick still get processed (each path is independent).
func TestStopToleratesError(t *testing.T) {
	const nowUnix = 1_700_000_000
	users := []store.UserSummary{
		{ID: "a", ContainerName: "homa-user-a", LastMessageAt: nowUnix - 70*60,
			NousPort: 40000, NousSessionID: "a", WorktreePath: "/w/a"},
		{ID: "b", ContainerName: "homa-user-b", LastMessageAt: nowUnix - 80*60,
			NousPort: 40002, NousSessionID: "b", WorktreePath: "/w/b"},
	}
	hub := newFakeHub()
	cm := &fakeCompactor{}
	gc, fm := buildGC(t, users, []string{"homa-user-a", "homa-user-b"}, hub, cm)
	fm.stopErrs["homa-user-a"] = errors.New("podman: connection refused")
	gc.SetNow(fixedNow(nowUnix))

	if err := gc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Both compacted; b stopped successfully, a's stop errored.
	if cm.callCount() != 2 {
		t.Errorf("compactor calls: got %d, want 2", cm.callCount())
	}
	if got := fm.stoppedSnapshot(); !equalStrings(got, []string{"homa-user-b"}) {
		t.Errorf("stopped: got %v, want [homa-user-b]", got)
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
