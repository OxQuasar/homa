// Package mainsite manages the orchestrator-owned public site sandbox.
// Unlike per-user sandboxes (one per signup, GC'd when idle), there's
// exactly one mainsite container; it serves the contents of site-template/
// at the orchestrator's root URL.
//
// Lifecycle:
//   - Start() runs Ensure once at boot and then re-runs it on a watchdog
//     ticker (default 30s). Ensure is idempotent: no-op when the container
//     is already running, respawn when it isn't.
//   - Stop() cancels the watchdog and explicitly stops the container.
//   - HostPort returns the host port to which the container's :5173 (vite)
//     is forwarded — the reverse proxy in cmd/homa wires / and any
//     unmatched paths through this port.
package mainsite

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
)

// ContainerName is the singleton container name. Picked statically so
// `podman ps`/`podman logs` work without a DB lookup.
const ContainerName = "homa-main"

// containerPreviewPort is the vite port inside the main container.
// Matches user sandboxes (entrypoint launches vite on 5173).
const containerPreviewPort = 5173

// watchdogInterval is how often Ensure re-runs to detect crashes. Cheap
// (one `podman inspect`) so a tighter interval is fine; 30s mirrors the
// proxy's per-WS last_active_at tick.
const watchdogInterval = 30 * time.Second

// Config supplies the static inputs the manager needs at startup.
type Config struct {
	// SiteTemplateDir is the absolute host path of the site-template
	// worktree (`main` branch). Bind-mounted into the container at
	// /workspace; vite serves files from there.
	SiteTemplateDir string

	// ImageRef is the podman image to run (shared with user sandboxes).
	ImageRef string

	// HostPort is the host-side port the container's vite (:5173) is
	// exposed on. Reverse proxy in cmd/homa forwards / to this port.
	HostPort int

	// MemoryLimit / CPULimit pass through to `podman run --memory=/--cpus=`.
	MemoryLimit string
	CPULimit    string
}

// Manager runs the mainsite sandbox and the watchdog that restarts it on
// crash. Construct with New(); call Start() to begin; Stop() to drain.
type Manager struct {
	sb  sandbox.Manager
	cfg Config
	log *slog.Logger

	stopFn context.CancelFunc
	done   chan struct{}
}

// New constructs a Manager. log may be nil → slog.Default().
func New(sb sandbox.Manager, cfg Config, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{sb: sb, cfg: cfg, log: log}
}

// HostPort returns the host port where the main container's vite is
// exposed. Reverse proxy uses this.
func (m *Manager) HostPort() int { return m.cfg.HostPort }

// Start brings the mainsite container up and launches a watchdog goroutine
// that re-Ensures every watchdogInterval. Idempotent — calling Start twice
// is undefined; don't.
func (m *Manager) Start(ctx context.Context) error {
	if m.cfg.SiteTemplateDir == "" || m.cfg.ImageRef == "" || m.cfg.HostPort <= 0 {
		return errors.New("mainsite: SiteTemplateDir, ImageRef, and HostPort are required")
	}
	if err := m.ensure(ctx); err != nil {
		// Initial start failure is logged but non-fatal — the reverse
		// proxy's ErrorHandler falls back to SPA login until the
		// watchdog brings the container up later.
		m.log.Error("mainsite: initial Ensure failed", "err", err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	m.stopFn = cancel
	m.done = make(chan struct{})
	go m.watchdog(loopCtx)
	return nil
}

// Stop cancels the watchdog and stops the container. Idempotent: safe to
// call even if Start failed.
func (m *Manager) Stop(ctx context.Context) {
	if m.stopFn != nil {
		m.stopFn()
		<-m.done
	}
	if err := m.sb.Stop(ctx, ContainerName); err != nil {
		m.log.Warn("mainsite: stop failed", "err", err)
	}
}

// ensure builds the Spec and asks sandbox.Manager.Ensure to bring the
// container up. No-op when already running.
func (m *Manager) ensure(ctx context.Context) error {
	spec := sandbox.Spec{
		ContainerName: ContainerName,
		ImageRef:      m.cfg.ImageRef,
		WorktreePath:  m.cfg.SiteTemplateDir,
		NousPort:      0, // main has no nous to expose
		PreviewPort:   m.cfg.HostPort,
		MemoryLimit:   m.cfg.MemoryLimit,
		CPULimit:      m.cfg.CPULimit,
		Env:           map[string]string{"HOMA_ROLE": "main"},
		NoAutoRemove:  true, // keep crashed container around for inspection
	}
	return m.sb.Ensure(ctx, spec)
}

// watchdog re-Ensures every interval until ctx fires. Recoveries from
// crashed containers cost one Ensure cycle's worth of latency.
func (m *Manager) watchdog(ctx context.Context) {
	defer close(m.done)
	t := time.NewTicker(watchdogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.ensure(ctx); err != nil {
				m.log.Warn("mainsite: watchdog Ensure failed", "err", err)
			}
		}
	}
}
