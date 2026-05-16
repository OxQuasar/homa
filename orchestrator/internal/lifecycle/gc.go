// Package lifecycle owns long-running background tasks for the
// orchestrator. Currently a single tick-driven loop that, for each user:
//
//   - emits an "idle compaction soon" warning frame to the browser ~1
//     minute before the threshold (so the editor can show a banner);
//   - at the threshold, force-disconnects the browser (releasing nous's
//     session lock), runs a full compaction in the sandbox, then stops
//     the container.
//
// "Idle" is measured against users.last_message_at — bumped only by
// actual user activity (login, sending a run request), NOT by the WS
// keepalive ticker. So a tab left open without messaging still rolls
// into the cold-and-compacted state.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// UserSummaryLister is the slice of *store.Store the GC needs.
type UserSummaryLister interface {
	ListUsers(ctx context.Context) ([]store.UserSummary, error)
}

// Hub is the slice of proxy.Hub the GC needs. Permits force-disconnect of
// browser WS and server-push of warning frames. May be nil — disables
// warnings + force-disconnect (test convenience; production wires it in).
type Hub interface {
	Disconnect(userID, reason string) int
	SendToUser(userID string, raw []byte) int
}

// Compactor is the slice the GC needs to run a full-compact on a user's
// nous before stopping their container. minTokens gates the actual
// compact: sessions at or below it return ErrBelowThreshold without
// triggering an LLM call. Tests substitute a stub.
type Compactor interface {
	Run(ctx context.Context, nousPort int, sessionID, workDir string, minTokens int64, timeout time.Duration) error
}

// GC drives the compact-then-stop idle lifecycle.
//
// Tick logic (per user, evaluated against last_message_at):
//
//   idle < (idleAfter - warningWindow): nothing
//   in last warningWindow before idleAfter: emit homa.idle_warning frame
//   idle >= idleAfter and container running: disconnect → compact → stop
//
// Best-effort throughout: Stop errors are logged at warn; compaction
// failures (busy session, timeout) don't block the Stop step.
type GC struct {
	sandbox         sandbox.Manager
	users           UserSummaryLister
	hub             Hub             // may be nil
	compactor       Compactor       // may be nil → compaction step skipped
	idleAfter       time.Duration
	warningWindow   time.Duration
	compactTimeout  time.Duration
	compactMinTokens int64
	interval        time.Duration
	now             func() time.Time
	log             *slog.Logger
}

// Config groups all lifecycle tunables.
type Config struct {
	IdleAfter        time.Duration
	WarningWindow    time.Duration // how long before IdleAfter to send the warning
	CompactTimeout   time.Duration // bound on the full_compact round-trip
	CompactMinTokens int64         // skip compaction when session PromptTokens <= this; 0 disables the gate
	Interval         time.Duration // ticker cadence
}

// New builds a GC. log may be nil → default. hub / compactor may be nil.
func New(sb sandbox.Manager, ul UserSummaryLister, hub Hub, compactor Compactor, cfg Config, log *slog.Logger) *GC {
	if log == nil {
		log = slog.Default()
	}
	return &GC{
		sandbox:          sb,
		users:            ul,
		hub:              hub,
		compactor:        compactor,
		idleAfter:        cfg.IdleAfter,
		warningWindow:    cfg.WarningWindow,
		compactTimeout:   cfg.CompactTimeout,
		compactMinTokens: cfg.CompactMinTokens,
		interval:         cfg.Interval,
		now:              func() time.Time { return time.Now().UTC() },
		log:              log,
	}
}

// SetNow overrides the time source (tests).
func (g *GC) SetNow(fn func() time.Time) { g.now = fn }

// Run blocks until ctx is cancelled, invoking tick() at `interval`. First
// tick fires immediately to catch staleness from prior runs.
func (g *GC) Run(ctx context.Context) error {
	g.log.Info("lifecycle gc started",
		"idle_after", g.idleAfter,
		"warning_window", g.warningWindow,
		"interval", g.interval)
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

func (g *GC) runTick(ctx context.Context) {
	if err := g.Tick(ctx); err != nil {
		g.log.Warn("gc tick failed", "err", err)
	}
}

// Tick scans users once and acts per the precedence rules. Exported for
// direct test invocation.
func (g *GC) Tick(ctx context.Context) error {
	users, err := g.users.ListUsers(ctx)
	if err != nil {
		return err
	}
	nowTs := g.now().UTC().Unix()
	stopCutoff := nowTs - int64(g.idleAfter.Seconds())
	warnCutoff := stopCutoff + int64(g.warningWindow.Seconds())

	for _, u := range users {
		switch {
		case u.LastMessageAt <= stopCutoff:
			g.compactAndStop(ctx, u)
		case u.LastMessageAt <= warnCutoff && g.hub != nil:
			secs := u.LastMessageAt + int64(g.idleAfter.Seconds()) - nowTs
			if secs < 0 {
				secs = 0
			}
			g.notifyWarning(u.ID, secs)
		}
	}
	return nil
}

// compactAndStop is the threshold-cross path: disconnect any open browser
// WS (releasing nous's session lock), run full_compact, stop the
// container. Each step is best-effort; failure logs and the chain
// continues so the Stop always runs.
func (g *GC) compactAndStop(ctx context.Context, u store.UserSummary) {
	running, err := g.sandbox.IsRunning(ctx, u.ContainerName)
	if err != nil {
		g.log.Warn("gc IsRunning failed", "user_id", u.ID, "container", u.ContainerName, "err", err)
		return
	}
	if !running {
		return
	}

	if g.hub != nil {
		if n := g.hub.Disconnect(u.ID, "idle compaction"); n > 0 {
			g.log.Info("gc disconnected browsers for compaction",
				"user_id", u.ID, "conns", n)
		}
	}

	if g.compactor != nil && u.NousSessionID != "" && u.NousPort > 0 {
		workDir := u.WorktreePath
		if workDir == "" {
			workDir = "/workspace"
		}
		g.log.Info("gc compacting",
			"user_id", u.ID, "session_id", u.NousSessionID, "nous_port", u.NousPort,
			"min_tokens", g.compactMinTokens)
		err := g.compactor.Run(ctx, u.NousPort, u.NousSessionID, workDir,
			g.compactMinTokens, g.compactTimeout)
		switch {
		case err == nil:
			g.log.Info("gc compaction complete", "user_id", u.ID)
		case errors.Is(err, ErrBelowThreshold):
			// Skip is the expected path for small sessions — log at
			// Info, not Warn. Stop still proceeds.
			g.log.Info("gc compaction skipped (session below threshold)",
				"user_id", u.ID, "min_tokens", g.compactMinTokens)
		default:
			// Non-fatal: Stop runs regardless.
			g.log.Warn("gc compaction failed; proceeding to stop",
				"user_id", u.ID, "err", err)
		}
	}

	if err := g.sandbox.Stop(ctx, u.ContainerName); err != nil {
		g.log.Warn("gc Stop failed", "user_id", u.ID, "container", u.ContainerName, "err", err)
		return
	}
	g.log.Info("gc stopped idle sandbox", "user_id", u.ID, "container", u.ContainerName,
		"last_message_at", u.LastMessageAt)
}

// notifyWarning pushes a synthetic homa.idle_warning frame to all
// browser tabs registered for this user. The editor renders a banner;
// see lib/types.ts for the wire shape. Failures here are silent — the
// banner is a nice-to-have, not load-bearing.
func (g *GC) notifyWarning(userID string, secondsLeft int64) {
	if secondsLeft < 0 {
		secondsLeft = 0
	}
	frame, err := json.Marshal(map[string]any{
		"type":                  "homa.idle_warning",
		"seconds_until_compact": secondsLeft,
	})
	if err != nil {
		return
	}
	g.hub.SendToUser(userID, frame)
}
