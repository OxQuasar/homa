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
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/config"
	"github.com/skipper/homa/orchestrator/internal/lifecycle"
	"github.com/skipper/homa/orchestrator/internal/mainsite"
	"github.com/skipper/homa/orchestrator/internal/provision"
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
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	// Subcommand dispatch. The default (no subcommand) runs the
	// orchestrator. `merge <userid>` is an admin-only operation: git
	// merge --no-ff user/<userid> into main inside site-template/.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "merge":
			if err := runMerge(os.Args[2:], log); err != nil {
				log.Error("merge failed", "err", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Fprint(os.Stderr, usageText)
			return
		}
	}

	configPath := flag.String("config", "config.json", "path to homa orchestrator config")
	flag.Parse()

	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

const usageText = `usage:
  homa [-config PATH]            run the orchestrator (default)
  homa merge <userid>            git-merge user/<userid> → main in site-template/
`

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
	siteTemplateDir, err := filepath.Abs(cfg.SiteTemplateDir)
	if err != nil {
		return fmt.Errorf("abs site_template_dir: %w", err)
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

	prov := buildProvisioner(cfg, branchesDir, siteTemplateDir, ports, st, log)

	authSvc := auth.New(st, prov, cfg.SecureCookies(), cfg.PreviewBaseURL, log)

	mux := http.NewServeMux()
	// Order: auth (POST endpoints + GET /me) → ws proxy (GET /ws) → static
	// (GET /signup, /login, /editor, /assets) → mainsite catch-all (GET /).
	// Method-aware mux means GET /signup and POST /signup coexist; the
	// mainsite catch-all only fires for GETs that didn't match a more
	// specific pattern.
	authSvc.Register(mux)
	proxy.Register(mux, st, authSvc, log)
	spaIndex, err := static.Register(mux, log)
	if err != nil {
		return fmt.Errorf("static.Register: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Mainsite — singleton sandbox running site-template/ on a known host
	// port. Reverse proxy at "/" forwards to it; on upstream failure
	// (vite not yet up, container crashed mid-tick) the SPA index handler
	// renders the SPA fallback so visitors see *something* instead of 502.
	var mainMgr *mainsite.Manager
	if cfg.MainSiteOn() {
		mainMgr = mainsite.New(
			sandbox.NewPodmanManager(cfg.PodmanBin, sandbox.ExecRunner{}),
			mainsite.Config{
				SiteTemplateDir: siteTemplateDir,
				ImageRef:        cfg.ImageRef,
				HostPort:        cfg.MainSiteHostPort,
				MemoryLimit:     cfg.ContainerMemory,
				CPULimit:        cfg.ContainerCPUs,
			},
			log,
		)
		if err := mainMgr.Start(ctx); err != nil {
			return fmt.Errorf("mainsite.Start: %w", err)
		}
		proxy.RegisterMainSite(mux, cfg.MainSiteHostPort, spaIndex, log)
		log.Info("mainsite", "enabled", true, "host_port", cfg.MainSiteHostPort,
			"site_template", siteTemplateDir)
	} else {
		// Without mainsite, GET / has no handler; serve the SPA so a
		// visitor lands somewhere coherent (preserves pre-mainsite
		// behavior of "bare host → SPA login flow").
		mux.Handle("GET /", spaIndex)
		log.Info("mainsite", "enabled", false)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

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
	if mainMgr != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		mainMgr.Stop(stopCtx)
	}
	log.Info("stopped")
	return nil
}

// buildProvisioner picks Stub vs Podman based on cfg.UsePodman, wires real
// ExecRunner-backed services for Podman, and logs the choice. The shared
// PortAllocator is supplied by the caller so it's been seeded from the users
// table before any signup hits it.
func buildProvisioner(cfg *config.Config, branchesDir, siteTemplateDir string, ports *provision.PortAllocator, st *store.Store, log *slog.Logger) provision.Provisioner {
	if !cfg.UsePodman {
		// Stub: just wire the same allocator so test parity holds. Port-start
		// args are ignored when the allocator is passed in.
		log.Info("provisioner", "kind", "stub")
		return newStubWithAllocator(branchesDir, ports)
	}

	apiKey := config.ExpandSecret(cfg.AnthropicAPIKey)
	if apiKey == "" {
		log.Warn("ANTHROPIC_API_KEY expanded to empty; sandbox API calls will fail until set")
	}

	credsPath := resolveClaudeCredentialsPath(cfg.ClaudeCredentialsPath, log)

	userConfigsDir, err := filepath.Abs(cfg.UserConfigsDir)
	if err != nil {
		log.Warn("abs UserConfigsDir failed; per-user configs disabled", "err", err)
		userConfigsDir = ""
	}
	nousConfigTemplate, err := filepath.Abs(cfg.NousConfigTemplate)
	if err != nil {
		log.Warn("abs NousConfigTemplate failed", "err", err)
		nousConfigTemplate = ""
	}

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
		UserConfigsDir:        userConfigsDir,
		NousConfigTemplate:    nousConfigTemplate,
		ReadinessTimeout:      time.Duration(cfg.ReadinessTimeoutSec) * time.Second,
		ReadinessInterval:     time.Duration(cfg.ReadinessIntervalMS) * time.Millisecond,
		Log:                   log,
	}
	log.Info("provisioner", "kind", "podman",
		"image", cfg.ImageRef, "site_template", siteTemplateDir, "branches", branchesDir,
		"claude_creds", credsPath,
		"user_configs_dir", userConfigsDir,
		"nous_config_template", nousConfigTemplate)
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

// userIDPattern validates the userid argument to `homa merge`. The 4-byte
// random hex id auth generates is exactly 8 lowercase-hex chars; reject
// anything else so a typo can't fall through to a shell command.
var userIDPattern = regexp.MustCompile(`^[a-f0-9]{8}$`)

// runMerge implements `homa merge <userid>`: git merge --no-ff user/<id>
// into main inside SiteTemplateDir. Admin-only; runs on the host with
// the operator's git identity. Conflicts surface as git's own non-zero
// exit + stderr — operator resolves by hand.
func runMerge(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: homa merge <userid>")
	}
	userID := args[0]
	if !userIDPattern.MatchString(userID) {
		return fmt.Errorf("invalid userid %q (want 8 lowercase hex chars)", userID)
	}

	cfg, err := config.Load("config.json")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	siteDir, err := filepath.Abs(cfg.SiteTemplateDir)
	if err != nil {
		return fmt.Errorf("abs site_template_dir: %w", err)
	}

	branch := "user/" + userID
	log.Info("merge: starting", "branch", branch, "into", "main", "repo", siteDir)

	cmd := exec.Command("git", "-C", siteDir, "merge", "--no-ff",
		"-m", "homa: merge "+branch, branch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git merge: %w (resolve conflicts in %s then retry)", err, siteDir)
	}
	log.Info("merge: success — mainsite vite will HMR to the new files")
	return nil
}
