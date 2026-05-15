// homa orchestrator entry point.
//
// Wires up the SQLite store, the chosen provisioner (Stub or Podman per
// cfg.UsePodman), auth + WS reverse proxy + embedded editor SPA on a
// single http.Server, plus the optional idle-sandbox GC goroutine.
// PortAllocator is seeded from the users table on startup so it survives
// daemon restarts. Configuration in ~/homa/RUNTIME.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/config"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/lifecycle"
	"github.com/skipper/homa/orchestrator/internal/proxy"
	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/static"
	"github.com/skipper/homa/orchestrator/internal/store"
	"github.com/skipper/homa/orchestrator/internal/tsserve"
	"github.com/skipper/homa/orchestrator/internal/worktree"
)

// shutdownGrace caps how long ListenAndServe takes to drain in-flight requests
// after SIGINT/SIGTERM before main returns.
const shutdownGrace = 10 * time.Second

func main() {
	configPath := flag.String("config", "config.json", "path to homa orchestrator config")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("mkdir data_dir: %w", err)
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	branchesDir, err := filepath.Abs(cfg.BranchesDir)
	if err != nil {
		return fmt.Errorf("abs branches_dir: %w", err)
	}
	hostStart := cfg.ProvisionHostPortStart
	if hostStart == 0 {
		hostStart = provision.HostPortStart
	}
	serveStart := cfg.ProvisionServePortStart
	if serveStart == 0 {
		serveStart = provision.PreviewServePortStart
	}

	// Build a shared PortAllocator and seed it from the users table — this
	// is the restart-safety guarantee: a second user signing up after a
	// daemon restart can't collide with already-allocated ports.
	ports := provision.NewPortAllocator(hostStart, serveStart)
	hostPorts, servePorts, err := st.AllUserPorts(context.Background())
	if err != nil {
		return fmt.Errorf("load user ports: %w", err)
	}
	ports.Seed(hostPorts, servePorts)
	log.Info("port allocator seeded",
		"users_in_db", len(servePorts),
		"max_host_port_seen", maxInt(hostPorts),
		"max_serve_port_seen", maxInt(servePorts))

	prov := buildProvisioner(cfg, branchesDir, ports, st, log)

	authSvc := auth.New(st, prov, cfg.SecureCookies(), cfg.PreviewBaseURL, log)

	mux := http.NewServeMux()
	// Order: auth (POST endpoints + GET /me) → proxy (GET /ws) → static
	// (GET /, /signup, /login, /editor, /assets). Method-aware mux means
	// GET /signup and POST /signup coexist on different handlers.
	authSvc.Register(mux)
	proxy.Register(mux, st, authSvc, log)
	if err := static.Register(mux, authSvc, log); err != nil {
		return fmt.Errorf("static.Register: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start the idle-sandbox GC if podman is on and the GC isn't explicitly
	// disabled. Best-effort background task; shares root ctx so it exits
	// on shutdown.
	if cfg.UsePodman && cfg.IdleAfterMinutes > 0 && cfg.GCIntervalSeconds > 0 {
		gc := lifecycle.New(
			sandbox.NewPodmanManager(cfg.PodmanBin, sandbox.ExecRunner{}),
			st,
			time.Duration(cfg.IdleAfterMinutes)*time.Minute,
			time.Duration(cfg.GCIntervalSeconds)*time.Second,
			log,
		)
		go func() {
			if err := gc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("lifecycle gc exited", "err", err)
			}
		}()
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
	case <-ctx.Done():
		log.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	log.Info("stopped")
	return nil
}

// buildProvisioner picks Stub vs Podman based on cfg.UsePodman, wires real
// ExecRunner-backed services for Podman, and logs the choice. The shared
// PortAllocator is supplied by the caller so it's been seeded from the users
// table before any signup hits it.
func buildProvisioner(cfg *config.Config, branchesDir string, ports *provision.PortAllocator, st *store.Store, log *slog.Logger) provision.Provisioner {
	if !cfg.UsePodman {
		// Stub: just wire the same allocator so test parity holds. Port-start
		// args are ignored when the allocator is passed in.
		log.Info("provisioner", "kind", "stub")
		return newStubWithAllocator(branchesDir, ports)
	}

	siteTemplateDir, err := filepath.Abs(cfg.SiteTemplateDir)
	if err != nil {
		// abs only fails if cwd is unreadable — extremely rare; fall back to raw value
		siteTemplateDir = cfg.SiteTemplateDir
	}

	apiKey := config.ExpandSecret(cfg.AnthropicAPIKey)
	if apiKey == "" {
		log.Warn("ANTHROPIC_API_KEY expanded to empty; sandbox API calls will fail until set")
	}

	credsPath := resolveClaudeCredentialsPath(cfg.ClaudeCredentialsPath, log)

	runner := sandbox.ExecRunner{}
	pp := &provision.PodmanProvisioner{
		Worktree:              worktree.New(cfg.GitBin, runner),
		Sandbox:               sandbox.NewPodmanManager(cfg.PodmanBin, runner),
		TSServe:               tsserve.New(cfg.TailscaleBin, runner),
		Users:                 st,
		Ports:                 ports,
		SiteTemplateDir:       siteTemplateDir,
		BranchesDir:           branchesDir,
		ImageRef:              cfg.ImageRef,
		PreviewBaseURL:        cfg.PreviewBaseURL,
		MemoryLimit:           cfg.ContainerMemory,
		CPULimit:              cfg.ContainerCPUs,
		AnthropicAPIKey:       apiKey,
		ClaudeCredentialsPath: credsPath,
		ReadinessTimeout:      time.Duration(cfg.ReadinessTimeoutSec) * time.Second,
		ReadinessInterval:     time.Duration(cfg.ReadinessIntervalMS) * time.Millisecond,
		Log:                   log,
	}
	log.Info("provisioner", "kind", "podman",
		"image", cfg.ImageRef, "site_template", siteTemplateDir, "branches", branchesDir,
		"claude_creds", credsPath)
	return pp
}

// resolveClaudeCredentialsPath maps the config field to a final path.
//   - "-"  → disabled (return "")
//   - ""   → auto-default to $HOME/.claude/.credentials.json
//   - else → used verbatim
// The provisioner re-probes existence on each Provision/EnsureRunning, so a
// non-existent default here doesn't block startup.
func resolveClaudeCredentialsPath(configured string, log *slog.Logger) string {
	if configured == "-" {
		return ""
	}
	if configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Warn("user home dir unavailable; claude credentials auto-mount disabled", "err", err)
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

// newStubWithAllocator wires a StubProvisioner against a pre-built (and
// possibly pre-seeded) allocator. Bypasses the public constructors so the
// allocator passed in by main.go is the single source of truth.
func newStubWithAllocator(branchesDir string, ports *provision.PortAllocator) provision.Provisioner {
	return provision.NewStubFromAllocator(branchesDir, ports)
}

// maxInt returns the largest int in xs, or 0 if xs is empty. Used for
// startup log lines summarising port-watermark seeding.
func maxInt(xs []int) int {
	m := 0
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}
