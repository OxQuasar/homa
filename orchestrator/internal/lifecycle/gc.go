// Package lifecycle owns long-running background tasks for the orchestrator.
// Currently: the idle-sandbox GC per mvp.md §16.
package lifecycle

import (
	"context"
	"log/slog"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// UserSummaryLister is the slice of *store.Store the GC needs. Narrow
// interface keeps tests light.
type UserSummaryLister interface {
	ListUsers(ctx context.Context) ([]store.UserSummary, error)
}

// GC stops idle per-user sandboxes. Runs on a ticker; each tick scans the
// users table, compares `now - last_active_at` against `idleAfter`, and
// stops any container whose user has been inactive for too long.
//
// "Active" is defined as "the proxy's last_active_at ticker bumped the row
// recently" — see internal/proxy/ws.go. The 30s ticker vs 30min default
// idleAfter gives a 60× safety margin against transient skips.
//
// Best-effort: Stop errors are logged at warn and the loop continues.
type GC struct {
	sandbox   sandbox.Manager
	users     UserSummaryLister
	idleAfter time.Duration
	interval  time.Duration
	now       func() time.Time // injectable for tests; defaults to time.Now
	log       *slog.Logger
}

// New builds a GC. `now` may be nil → time.Now. `log` may be nil → default.
func New(sb sandbox.Manager, ul UserSummaryLister, idleAfter, interval time.Duration, log *slog.Logger) *GC {
	if log == nil {
		log = slog.Default()
	}
	return &GC{
		sandbox:   sb,
		users:     ul,
		idleAfter: idleAfter,
		interval:  interval,
		now:       func() time.Time { return time.Now().UTC() },
		log:       log,
	}
}

// SetNow overrides the time source (tests).
func (g *GC) SetNow(fn func() time.Time) { g.now = fn }

// Run blocks until ctx is cancelled, invoking tick() at `interval`. The
// first tick fires immediately so idle containers get cleaned up on
// orchestrator startup (after a long downtime, users may already be stale).
func (g *GC) Run(ctx context.Context) error {
	g.log.Info("lifecycle gc started", "idle_after", g.idleAfter, "interval", g.interval)
	// Initial tick to catch staleness from prior runs.
	g.runTick(ctx)
	t := time.NewTicker(g.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			g.log.Info("lifecycle gc stopped")
			return ctx.Err()
		case <-t.C:
			g.runTick(ctx)
		}
	}
}

// runTick wraps Tick with error logging so the loop itself is silent.
func (g *GC) runTick(ctx context.Context) {
	if err := g.Tick(ctx); err != nil {
		g.log.Warn("gc tick failed", "err", err)
	}
}

// Tick walks the users table once and stops anyone past idleAfter.
// Exported for direct invocation from tests.
func (g *GC) Tick(ctx context.Context) error {
	users, err := g.users.ListUsers(ctx)
	if err != nil {
		return err
	}
	cutoff := g.now().UTC().Unix() - int64(g.idleAfter.Seconds())
	for _, u := range users {
		if u.LastActiveAt > cutoff {
			continue
		}
		running, err := g.sandbox.IsRunning(ctx, u.ContainerName)
		if err != nil {
			g.log.Warn("gc IsRunning failed", "user_id", u.ID, "container", u.ContainerName, "err", err)
			continue
		}
		if !running {
			continue
		}
		if err := g.sandbox.Stop(ctx, u.ContainerName); err != nil {
			g.log.Warn("gc Stop failed", "user_id", u.ID, "container", u.ContainerName, "err", err)
			continue
		}
		g.log.Info("gc stopped idle sandbox", "user_id", u.ID, "container", u.ContainerName, "last_active_at", u.LastActiveAt)
	}
	return nil
}
