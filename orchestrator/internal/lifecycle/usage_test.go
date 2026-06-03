package lifecycle

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// fakeSnapshotter returns a canned snapshot per nous port. Used to
// test the UsageLogger without standing up a real nous WS.
type fakeSnapshotter struct {
	byPort  map[int]Snapshot
	calls   atomic.Int32
	errPort int // if matches, return error
}

func (f *fakeSnapshotter) Snapshot(ctx context.Context, port int, sid, wd string, _ time.Duration) (Snapshot, error) {
	f.calls.Add(1)
	if port == f.errPort {
		return Snapshot{}, context.DeadlineExceeded
	}
	if s, ok := f.byPort[port]; ok {
		return s, nil
	}
	return Snapshot{}, nil
}

// fakeUsageStore implements UsageUserLister.
type fakeUsageStore struct {
	users []store.UserSummary
	full  map[string]*store.User
}

func (f *fakeUsageStore) ListUsers(ctx context.Context) ([]store.UserSummary, error) {
	return f.users, nil
}
func (f *fakeUsageStore) GetUserByID(ctx context.Context, id string) (*store.User, error) {
	if u, ok := f.full[id]; ok {
		return u, nil
	}
	return nil, store.ErrNotFound
}

// (fakeSandbox is defined below — it's the lifecycle test's shared
// stub for sandbox.Manager.)

// Tick walks all users, queries only the running ones, and logs each
// at INFO with "usage:". Skipped users (not running) get no log line.
func TestUsageLogger_Tick_LogsOnlyRunningUsers(t *testing.T) {
	store := &fakeUsageStore{
		users: []store.UserSummary{
			{ID: "user1111", ContainerName: "homa-user-user1111"},
			{ID: "user2222", ContainerName: "homa-user-user2222"},
			{ID: "user3333", ContainerName: "homa-user-user3333"},
		},
		full: map[string]*storeUser{
			"user1111": {ID: "user1111", Username: "alice", NousPort: 40000, NousSessionID: "s1", ContainerName: "homa-user-user1111", WorktreePath: "/w1"},
			"user2222": {ID: "user2222", Username: "bob", NousPort: 40001, NousSessionID: "s2", ContainerName: "homa-user-user2222", WorktreePath: "/w2"},
			"user3333": {ID: "user3333", Username: "carol", NousPort: 40002, NousSessionID: "s3", ContainerName: "homa-user-user3333", WorktreePath: "/w3"},
		},
	}
	sb := &fakeSandbox{running: map[string]bool{
		"homa-user-user1111": true,
		"homa-user-user2222": false, // stopped
		"homa-user-user3333": true,
	}}
	snap := &fakeSnapshotter{byPort: map[int]Snapshot{
		40000: {PromptTokens: 10_000, CacheCreationTokens: 2_000, CacheReadTokens: 50_000},
		40002: {PromptTokens: 5_000, CacheReadTokens: 1_000},
	}}
	logBuf := &countingHandler{level: slog.LevelInfo}
	log := slog.New(logBuf)

	u := NewUsageLogger(sb, store, snap, UsageLoggerConfig{Interval: time.Hour, Timeout: time.Second}, log)
	u.Tick(context.Background())

	if got := snap.calls.Load(); got != 2 {
		t.Errorf("snapshot calls: got %d, want 2 (alice + carol; bob stopped)", got)
	}
	if got := logBuf.usageLines; got != 2 {
		t.Errorf("'usage:' log lines: got %d, want 2", got)
	}
}

// SnapshotterErrors don't crash the tick — failed users are skipped
// at debug level + the rest continue.
func TestUsageLogger_Tick_SnapshotErrorIsSoft(t *testing.T) {
	store := &fakeUsageStore{
		users: []store.UserSummary{
			{ID: "user1111", ContainerName: "homa-user-user1111"},
			{ID: "user2222", ContainerName: "homa-user-user2222"},
		},
		full: map[string]*storeUser{
			"user1111": {ID: "user1111", Username: "alice", NousPort: 40000, NousSessionID: "s1", ContainerName: "homa-user-user1111", WorktreePath: "/w1"},
			"user2222": {ID: "user2222", Username: "bob", NousPort: 40001, NousSessionID: "s2", ContainerName: "homa-user-user2222", WorktreePath: "/w2"},
		},
	}
	sb := &fakeSandbox{running: map[string]bool{
		"homa-user-user1111": true,
		"homa-user-user2222": true,
	}}
	snap := &fakeSnapshotter{
		byPort:  map[int]Snapshot{40000: {PromptTokens: 1000}},
		errPort: 40001, // bob's port errors
	}
	logBuf := &countingHandler{level: slog.LevelInfo}
	log := slog.New(logBuf)

	u := NewUsageLogger(sb, store, snap, UsageLoggerConfig{Interval: time.Hour, Timeout: time.Second}, log)
	u.Tick(context.Background())

	if got := snap.calls.Load(); got != 2 {
		t.Errorf("snapshot calls: got %d, want 2 (both users attempted)", got)
	}
	if got := logBuf.usageLines; got != 1 {
		t.Errorf("'usage:' log lines at INFO: got %d, want 1 (only alice succeeded)", got)
	}
}

// --- helpers below: types we reference + the slog handler that counts ---

type storeUser = store.User

// fakeSandbox satisfies sandbox.Manager. UsageLogger only calls
// IsRunning, so the rest are stubs.
type fakeSandbox struct {
	running map[string]bool
}

func (f *fakeSandbox) IsRunning(ctx context.Context, name string) (bool, error) {
	return f.running[name], nil
}
func (f *fakeSandbox) Ensure(ctx context.Context, spec sandbox.Spec) error { return nil }
func (f *fakeSandbox) Stop(ctx context.Context, name string) error         { return nil }
func (f *fakeSandbox) Logs(ctx context.Context, name string, lines int) ([]string, error) {
	return nil, nil
}

// countingHandler is a slog.Handler that counts "usage:" log lines so
// the test asserts on log emissions without parsing text.
type countingHandler struct {
	level      slog.Level
	usageLines int
}

func (h *countingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelInfo && r.Message == "usage:" {
		h.usageLines++
	}
	return nil
}
func (h *countingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(_ string) slog.Handler      { return h }

var _ slog.Handler = (*countingHandler)(nil)
var _ = io.Discard
