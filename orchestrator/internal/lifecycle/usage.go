// Per-user usage logger — periodically samples each running user's
// nous for token-usage stats and emits a structured log line.
//
// Purpose is observation, not enforcement: the operator greps
// journalctl to see who's heavy. No DB persistence; the snapshot is
// ephemeral.
//
//   journalctl --user -u homa | grep usage:
//   → usage: user_id=77b4cf0e session_id=66372906 prompt_tokens=12345 cache_creation_tokens=2103 cache_read_tokens=45231
//
// Independent of the lifecycle GC tick — different cadence (5min vs
// 60s default) and different concerns (cost observation vs idle
// reclamation).

package lifecycle

import (
	"context"
	"log/slog"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// Snapshotter is the small interface the UsageLogger needs from the
// nous-comms layer. Implemented by CompactClient in production; mocked
// in tests.
type Snapshotter interface {
	Snapshot(ctx context.Context, nousPort int, sessionID, workDir string, timeout time.Duration) (Snapshot, error)
}

// UsageLogger samples + logs per-user token usage periodically.
type UsageLogger struct {
	sandbox     sandbox.Manager
	users       UsageUserLister
	snapshotter Snapshotter
	interval    time.Duration
	timeout     time.Duration // per-user dial+snapshot timeout
	log         *slog.Logger
}

// UsageLoggerConfig keeps construction tidy.
type UsageLoggerConfig struct {
	Interval time.Duration // tick cadence (e.g. 5*time.Minute)
	Timeout  time.Duration // per-user snapshot timeout (e.g. 5*time.Second)
}

// NewUsageLogger constructs the logger. snapshotter may be nil →
// no-op tick (still loops but never queries). log defaults to slog.Default.
func NewUsageLogger(sb sandbox.Manager, users UsageUserLister, snapshotter Snapshotter, cfg UsageLoggerConfig, log *slog.Logger) *UsageLogger {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &UsageLogger{
		sandbox: sb, users: users, snapshotter: snapshotter,
		interval: cfg.Interval, timeout: cfg.Timeout, log: log,
	}
}

// Run blocks until ctx is cancelled. Each tick walks all users + logs
// usage for those whose containers are up.
func (u *UsageLogger) Run(ctx context.Context) error {
	u.log.Info("usage logger started", "interval", u.interval, "timeout", u.timeout)
	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()
	for {
		// Tick at startup too, not just after the first interval — so
		// the operator sees baseline numbers immediately after a restart.
		u.Tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Tick samples once across all running users. Public for tests.
func (u *UsageLogger) Tick(ctx context.Context) {
	if u.snapshotter == nil {
		return
	}
	summaries, err := u.users.ListUsers(ctx)
	if err != nil {
		u.log.Warn("usage: list users", "err", err)
		return
	}
	for _, s := range summaries {
		// Skip users without an active sandbox — no nous to query.
		running, err := u.sandbox.IsRunning(ctx, s.ContainerName)
		if err != nil {
			u.log.Debug("usage: IsRunning check failed", "user_id", s.ID, "err", err)
			continue
		}
		if !running {
			continue
		}
		// Fetch full user record for the fields we need.
		full, err := u.users.GetUserByID(ctx, s.ID)
		if err != nil {
			u.log.Debug("usage: GetUserByID failed", "user_id", s.ID, "err", err)
			continue
		}
		if full.NousPort == 0 || full.NousSessionID == "" {
			continue
		}
		snap, err := u.snapshotter.Snapshot(ctx, full.NousPort, full.NousSessionID, full.WorktreePath, u.timeout)
		if err != nil {
			// Common: container is up but nous isn't ready yet (e.g.
			// startup race). Debug-level so we don't spam logs.
			u.log.Debug("usage: snapshot failed", "user_id", s.ID, "err", err)
			continue
		}
		u.log.Info("usage:",
			"user_id", s.ID,
			"username", full.Username,
			"session_id", full.NousSessionID,
			"prompt_tokens", snap.PromptTokens,
			"cache_creation_tokens", snap.CacheCreationTokens,
			"cache_read_tokens", snap.CacheReadTokens,
		)
	}
}

// UserSummaryLister: refines what UsageLogger needs from the store.
// Bigger interface than gc.go's UserSummaryLister because we also need
// the full user record for NousPort + NousSessionID. Embeds the existing
// list interface for compatibility.
//
// (Note: gc.go's UserSummaryLister is just `ListUsers`. We extend it
// here without modifying gc's expectations.)
type UsageUserLister interface {
	ListUsers(ctx context.Context) ([]store.UserSummary, error)
	GetUserByID(ctx context.Context, id string) (*store.User, error)
}
