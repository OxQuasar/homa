// homa orchestrator entry point.
//
// Wires up the SQLite store, the chosen provisioner (Stub or Podman per
// cfg.UsePodman), auth + WS reverse proxy + embedded editor SPA on a
// single http.Server, plus the optional idle-sandbox GC goroutine.
// PortAllocator is seeded from the users table on startup so it survives
// daemon restarts. Configuration in ~/homa/RUNTIME.md.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/codeserver"
	"github.com/skipper/homa/orchestrator/internal/codeurl"
	"github.com/skipper/homa/orchestrator/internal/config"
	"github.com/skipper/homa/orchestrator/internal/cors"
	"github.com/skipper/homa/orchestrator/internal/forum"
	"github.com/skipper/homa/orchestrator/internal/messages"
	"github.com/skipper/homa/orchestrator/internal/usersapi"
	"github.com/skipper/homa/orchestrator/internal/lifecycle"
	"github.com/skipper/homa/orchestrator/internal/mainsite"
	"github.com/skipper/homa/orchestrator/internal/prflow"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/proxy"
	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/static"
	"github.com/skipper/homa/orchestrator/internal/store"
	"github.com/skipper/homa/orchestrator/internal/tsserve"
	"github.com/skipper/homa/orchestrator/internal/upload"
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
		case "list":
			if err := runList(os.Args[2:], log); err != nil {
				log.Error("list failed", "err", err)
				os.Exit(1)
			}
			return
		case "review":
			if err := runReview(os.Args[2:], log); err != nil {
				log.Error("review failed", "err", err)
				os.Exit(1)
			}
			return
		case "reload":
			if err := runReload(os.Args[2:], log); err != nil {
				log.Error("reload failed", "err", err)
				os.Exit(1)
			}
			return
		case "pr":
			if err := runPR(os.Args[2:], log); err != nil {
				log.Error("pr failed", "err", err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Fprint(os.Stderr, usageText)
			return
		}
	}

	configPath := flag.String("config", defaultConfigPath(), "path to homa orchestrator config (or set HOMA_CONFIG)")
	flag.Parse()

	if err := run(*configPath, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

const usageText = `usage:
  homa [-config PATH]            run the orchestrator (default)
  homa merge <userid>            git-merge user/<userid> → main in site-template/
  homa list                      print all users as 'email | userid' (sorted by created_at)
  homa review <userid>           print review URLs (preview + vscode) + diff/merge commands
  homa reload <userid>           stop user's container — next login respawns with current config

  homa pr list                   list all pr/<userid>/<topic> branches with stats vs main
  homa pr show [<branch>]        diff + commits for a PR branch (no arg = single open PR)
  homa pr merge <branch>         git-merge a PR branch into main (same flow as 'homa merge')
  homa pr close <branch>         delete a PR branch without merging
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

	// Code-server secret — only loaded if the feature is enabled. The
	// presence of a non-empty []byte is what tells the provisioner to
	// launch code-server in each user's sandbox.
	var codeServerSecret []byte
	if cfg.CodeServerOn() {
		csPath, perr := filepath.Abs(cfg.CodeServerSecretPath)
		if perr != nil {
			return fmt.Errorf("abs code_server_secret_path: %w", perr)
		}
		s, serr := codeserver.LoadOrCreateSecret(csPath)
		if serr != nil {
			return fmt.Errorf("code-server secret: %w", serr)
		}
		codeServerSecret = s
		log.Info("code-server", "enabled", true, "secret_path", csPath)
		// One-shot startup backfill: any user whose code_server_port is 0
		// (pre-feature row) gets ports allocated now. Existing running
		// containers don't yet publish the port; they'll pick it up on
		// next respawn (idle GC, image rebuild, or operator restart).
		if err := backfillCodeServerPorts(context.Background(), st, ports, log); err != nil {
			log.Warn("code-server: port backfill failed; existing users won't get IDE until fixed",
				"err", err)
		}
	} else {
		log.Info("code-server", "enabled", false)
	}

	prov := buildProvisioner(cfg, branchesDir, siteTemplateDir, ports, st, codeServerSecret, log)

	authSvc := auth.New(st, prov, cfg.SecureCookies(), cfg.PreviewBaseURL, log)

	mux := http.NewServeMux()
	// Order: auth (POST endpoints + GET /me) → ws proxy (GET /ws) → static
	// (GET /signup, /login, /editor, /assets) → mainsite catch-all (GET /).
	// Method-aware mux means GET /signup and POST /signup coexist; the
	// mainsite catch-all only fires for GETs that didn't match a more
	// specific pattern.
	// CORS policy for cross-origin API access from user-iframe sites.
	// User sites render at <PreviewBaseURL>:<user-port>/; need to call
	// orchestrator APIs at <PreviewBaseURL>:443/. Same host, different
	// ports → cross-origin. Policy allows any port on the configured host.
	corsPolicy := cors.New(cfg.PreviewBaseURL)
	authSvc.Register(mux, corsPolicy.Middleware)
	hub := proxy.NewHub(log)
	proxy.Register(mux, st, authSvc, hub, log)
	upload.New(branchesDir, cfg.UploadMaxBytes, log).Register(mux, authSvc)

	// Code-server URL endpoint: returns per-user {url, enabled} for the
	// editor's "Open VS Code" button. When the feature is disabled
	// returns {enabled: false} so the editor hides the button.
	codeServerHost := resolveCodeServerHost(cfg, log)
	codeurl.NewHandler(codeServerHost, codeServerSecret, log).Register(mux, authSvc)

	// Forum endpoints. Auth-required (reads + writes); CORS-wrapped so
	// the user's iframe-served forum page can call them cross-origin
	// from <PreviewBaseURL>:<user-port>.
	forum.New(forum.NewStore(st.DB()), log).Register(mux, authSvc, corsPolicy.Middleware)

	// Public people directory — same auth + CORS posture as forum.
	usersapi.New(st, log).Register(mux, authSvc, corsPolicy.Middleware)

	// Direct messages between users. Same auth + CORS posture.
	messages.New(messages.NewStore(st.DB()), log).Register(mux, authSvc, corsPolicy.Middleware)
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

	// Start the lifecycle loop: per-user idle compaction + container stop.
	// Best-effort background task; shares root ctx so it exits on shutdown.
	// Gated on podman + positive thresholds so the stub provisioner path
	// and explicit disable both skip cleanly.
	if cfg.UsePodman && cfg.IdleAfterMinutes > 0 && cfg.GCIntervalSeconds > 0 {
		gc := lifecycle.New(
			sandbox.NewPodmanManager(cfg.PodmanBin, sandbox.ExecRunner{}),
			st,
			hub,
			lifecycle.CompactClient{},
			lifecycle.Config{
				IdleAfter:        time.Duration(cfg.IdleAfterMinutes) * time.Minute,
				WarningWindow:    time.Duration(cfg.IdleWarningSeconds) * time.Second,
				CompactTimeout:   time.Duration(cfg.CompactTimeoutSeconds) * time.Second,
				CompactMinTokens: cfg.CompactMinTokens,
				Interval:         time.Duration(cfg.GCIntervalSeconds) * time.Second,
			},
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
func buildProvisioner(cfg *config.Config, branchesDir, siteTemplateDir string, ports *provision.PortAllocator, st *store.Store, codeServerSecret []byte, log *slog.Logger) provision.Provisioner {
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
		CodeServerSecret:      codeServerSecret,
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

// runMerge implements `homa merge <userid>`:
//   1. Auto-commit uncommitted work in branches/<userid>/ (everything that
//      `git status --porcelain` would show — modified, added, deleted,
//      untracked). Author is the user's email from the DB; falls back to
//      a synthetic homa-bot identity if the store lookup fails.
//   2. git merge --no-ff user/<id> into main inside SiteTemplateDir.
//
// Admin-only; runs on the host. Conflicts on step 2 surface as git's own
// non-zero exit + stderr — operator resolves by hand.
func runMerge(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: homa merge <userid>")
	}
	userID := args[0]
	if !userIDPattern.MatchString(userID) {
		return fmt.Errorf("invalid userid %q (want 8 lowercase hex chars)", userID)
	}

	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	siteDir, err := filepath.Abs(cfg.SiteTemplateDir)
	if err != nil {
		return fmt.Errorf("abs site_template_dir: %w", err)
	}
	branchesDir, err := filepath.Abs(cfg.BranchesDir)
	if err != nil {
		return fmt.Errorf("abs branches_dir: %w", err)
	}
	worktreePath := filepath.Join(branchesDir, userID)

	// Step 1: auto-commit. Best-effort store lookup for a real email;
	// fall back to a synthetic identity so the merge still works if the
	// DB is missing / locked / etc.
	commitEmail := "homa-bot@homa.local"
	commitName := "homa-bot (user " + userID + ")"
	if st, err := store.Open(cfg.DBPath()); err == nil {
		defer st.Close()
		if u, lerr := st.GetUserByID(context.Background(), userID); lerr == nil {
			commitEmail = u.Email
			commitName = u.Email
		} else {
			log.Warn("merge: user lookup failed; using synthetic commit author",
				"user_id", userID, "err", lerr)
		}
	} else {
		log.Warn("merge: store unavailable; using synthetic commit author", "err", err)
	}
	if err := autoCommit(worktreePath, commitEmail, commitName, log); err != nil {
		return fmt.Errorf("auto-commit: %w", err)
	}

	// Step 2: merge into main.
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

// runList implements `homa list`: prints every user as
//   <email> | <userid>
// to stdout, one per line, sorted by created_at (oldest first — matches
// signup order). Header line is skipped so output is greppable / pipeable.
//
// Output goes to stdout; the orchestrator's slog logs to stderr, so a
// `homa list | head -1` works cleanly even with logs enabled.
//
// Read-only — does not start the orchestrator or touch container state.
func runList(args []string, log *slog.Logger) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: homa list (takes no arguments)")
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	users, err := st.ListUsers(context.Background())
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	// ListUsers returns the lifecycle projection (no email); fetch each
	// row's full record. n is small (single-operator deployments), so
	// the per-row query is fine; not worth a custom email-only query.
	for _, summary := range users {
		u, err := st.GetUserByID(context.Background(), summary.ID)
		if err != nil {
			log.Warn("list: user lookup failed", "user_id", summary.ID, "err", err)
			continue
		}
		fmt.Printf("%s | %s\n", u.Email, u.ID)
	}
	return nil
}

// runReview implements `homa review <userid>`: prints the operator's
// review surface for a user — preview URL, code-server URL, the host
// `git diff` command, and the merge command. No DB writes, no
// container ops. Pure information.
//
// Both URLs are accessible to anyone on the operator's tailnet (the
// tailscale-serve registration is the only gate); the editor's cookie
// gate doesn't apply to them. So the operator can open them in a
// browser tab and see the user's site + their VS Code workspace.
func runReview(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: homa review <userid>")
	}
	userID := args[0]
	if !userIDPattern.MatchString(userID) {
		return fmt.Errorf("invalid userid %q (want 8 lowercase hex chars)", userID)
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	u, err := st.GetUserByID(context.Background(), userID)
	if err != nil {
		return fmt.Errorf("get user %s: %w", userID, err)
	}

	host := resolveCodeServerHost(cfg, log)
	previewURL := ""
	vscodeURL := ""
	if cfg.PreviewBaseURL != "" && u.PreviewServePort > 0 {
		previewURL = fmt.Sprintf("%s:%d/", cfg.PreviewBaseURL, u.PreviewServePort)
	}
	if host != "" && u.CodeServerServePort > 0 {
		vscodeURL = fmt.Sprintf("https://%s:%d/?folder=/workspace", host, u.CodeServerServePort)
	}

	siteDir, _ := filepath.Abs(cfg.SiteTemplateDir)
	fmt.Printf("user:    %s (%s)\n", u.Email, u.ID)
	if previewURL != "" {
		fmt.Printf("preview: %s\n", previewURL)
	} else {
		fmt.Printf("preview: (unavailable — PreviewBaseURL or port not configured)\n")
	}
	if vscodeURL != "" {
		fmt.Printf("vscode:  %s\n", vscodeURL)
	} else {
		fmt.Printf("vscode:  (unavailable — code-server disabled or port not allocated)\n")
	}
	fmt.Printf("diff:    git -C %s diff main..user/%s\n", siteDir, u.ID)
	fmt.Printf("merge:   ./homa merge %s\n", u.ID)
	return nil
}

// runReload implements `homa reload <userid>`: podman-stops the user's
// container. Their next /login or /ws hit triggers EnsureRunning which
// respawns the container — picking up any host-side changes to the
// per-user nous config, the sandbox image, the Claude credentials, etc.
//
// Persistent state (chat history, code-server settings, installed
// extensions) survives via the per-user podman volumes; --rm only
// destroys the running container, not the named volumes.
func runReload(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: homa reload <userid>")
	}
	userID := args[0]
	if !userIDPattern.MatchString(userID) {
		return fmt.Errorf("invalid userid %q (want 8 lowercase hex chars)", userID)
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	u, err := st.GetUserByID(context.Background(), userID)
	if err != nil {
		return fmt.Errorf("get user %s: %w", userID, err)
	}
	log.Info("reload: stopping container", "user_id", u.ID, "container", u.ContainerName)
	cmd := exec.Command(cfg.PodmanBin, "stop", "-t", "5", u.ContainerName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// 'no such container' is fine — already stopped; next login
		// respawns regardless. Other errors surface as failures.
		return fmt.Errorf("podman stop: %w", err)
	}
	log.Info("reload: container stopped; next /login or /ws respawns with current config",
		"user_id", u.ID, "container", u.ContainerName)
	return nil
}

// autoCommit stages and commits everything in the user's worktree if dirty.
// No-op when worktree is clean. Commit author is supplied by the caller.
func autoCommit(worktreePath, email, name string, log *slog.Logger) error {
	statusOut, err := exec.Command("git", "-C", worktreePath, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("git status in %s: %w", worktreePath, err)
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		log.Info("auto-commit: worktree clean", "path", worktreePath)
		return nil
	}

	files := bytes.Count(statusOut, []byte("\n"))
	log.Info("auto-commit: staging changes", "path", worktreePath, "files", files,
		"author_email", email)

	if err := exec.Command("git", "-C", worktreePath, "add", "-A").Run(); err != nil {
		return fmt.Errorf("git add -A in %s: %w", worktreePath, err)
	}
	commit := exec.Command("git", "-C", worktreePath,
		"-c", "user.email="+email,
		"-c", "user.name="+name,
		"commit", "-m", "homa: auto-commit before merge")
	commit.Stdout = os.Stdout
	commit.Stderr = os.Stderr
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit in %s: %w", worktreePath, err)
	}
	return nil
}

// resolveCodeServerHost returns the hostname code-server URLs should
// target. Priority: explicit cfg.CodeServerHost > host parsed from
// cfg.PreviewBaseURL > "" (handler will then disable the feature).
//
// The URL the editor opens is `https://<host>:<port>/?…`; port comes
// from each user's CodeServerServePort, not from PreviewBaseURL.
func resolveCodeServerHost(cfg *config.Config, log *slog.Logger) string {
	if cfg.CodeServerHost != "" {
		return cfg.CodeServerHost
	}
	if cfg.PreviewBaseURL == "" {
		return ""
	}
	u, err := url.Parse(cfg.PreviewBaseURL)
	if err != nil {
		log.Warn("code-server host parse failed; URL endpoint will be disabled",
			"preview_base_url", cfg.PreviewBaseURL, "err", err)
		return ""
	}
	// url.Parse keeps host:port in Host; we want host-only since port
	// comes from per-user CodeServerServePort. Strip the port if any.
	h := u.Hostname()
	if h == "" {
		// PreviewBaseURL might be schema-less ("gandiva.tailnet.ts.net").
		// Strip "://" prefix manually as a fallback.
		h = strings.TrimPrefix(cfg.PreviewBaseURL, "https://")
		h = strings.TrimPrefix(h, "http://")
		if i := strings.IndexByte(h, ':'); i > 0 {
			h = h[:i]
		}
		if i := strings.IndexByte(h, '/'); i > 0 {
			h = h[:i]
		}
	}
	return h
}

// backfillCodeServerPorts scans the users table for rows where the
// code-server feature is on but the user predates it (code_server_port == 0).
// Allocates ports via the (post-seed) PortAllocator and writes them back.
// Existing containers don't pick up the new port until next respawn —
// the operator may want to `podman stop` those users so they get IDE
// access on next login. Logs an Info per row updated.
//
// Idempotent: re-running on an already-backfilled DB is a no-op.
func backfillCodeServerPorts(ctx context.Context, st *store.Store, ports *provision.PortAllocator, log *slog.Logger) error {
	users, err := st.ListUsers(ctx)
	if err != nil {
		return fmt.Errorf("ListUsers: %w", err)
	}
	updated := 0
	for _, u := range users {
		row, err := st.GetUserByID(ctx, u.ID)
		if err != nil {
			log.Warn("backfill: GetUserByID failed", "user_id", u.ID, "err", err)
			continue
		}
		if row.CodeServerPort > 0 && row.CodeServerServePort > 0 {
			continue
		}
		hostPort := ports.NextHostPort()
		servePort := ports.NextServePort()
		if err := st.SetCodeServerPorts(ctx, u.ID, hostPort, servePort); err != nil {
			log.Warn("backfill: SetCodeServerPorts failed", "user_id", u.ID, "err", err)
			continue
		}
		log.Info("backfill: allocated code-server ports",
			"user_id", u.ID, "host_port", hostPort, "serve_port", servePort)
		updated++
	}
	if updated > 0 {
		log.Info("backfill: complete — existing users get IDE access on next container respawn",
			"updated", updated)
	}
	return nil
}

// --- PR subcommands ---------------------------------------------------
//
// Phase 1 PRs are pure git convention: branches matching pr/<userid>/<topic>
// are pull requests. No DB, no API. `homa pr {list,show,merge,close}` is
// the operator's interface. See internal/prflow/.

func runPR(args []string, log *slog.Logger) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: homa pr {list|show|merge|close} [<branch>]")
	}
	switch args[0] {
	case "list":
		return runPRList(args[1:], log)
	case "show":
		return runPRShow(args[1:], log)
	case "merge":
		return runPRMerge(args[1:], log)
	case "close":
		return runPRClose(args[1:], log)
	default:
		return fmt.Errorf("unknown pr subcommand %q; want list/show/merge/close", args[0])
	}
}

// runPRList enumerates pr/* branches with author + stats vs main.
func runPRList(args []string, log *slog.Logger) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: homa pr list (takes no args)")
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	siteDir, _ := filepath.Abs(cfg.SiteTemplateDir)

	prs, err := prflow.List(cfg.GitBin, siteDir, "main")
	if err != nil {
		return fmt.Errorf("list PR branches: %w", err)
	}
	if len(prs) == 0 {
		fmt.Println("(no PR branches)")
		return nil
	}

	// Resolve usernames best-effort. If store unavailable, fall back
	// to userid in the author column.
	usernames := map[string]string{}
	if st, err := store.Open(cfg.DBPath()); err == nil {
		defer st.Close()
		for _, p := range prs {
			if _, seen := usernames[p.UserID]; seen {
				continue
			}
			if u, err := st.GetUserByID(context.Background(), p.UserID); err == nil {
				usernames[p.UserID] = u.Username
			}
		}
	}

	// Sorted by branch name (deterministic output).
	sort.Slice(prs, func(i, j int) bool { return prs[i].Name < prs[j].Name })

	fmt.Printf("%-40s %-12s %5s %5s %s\n", "BRANCH", "AUTHOR", "AHEAD", "FILES", "+/-")
	for _, p := range prs {
		author := usernames[p.UserID]
		if author == "" {
			author = p.UserID
		}
		fmt.Printf("%-40s %-12s %5d %5d +%d/-%d\n",
			p.Name, author,
			p.Stats.CommitsAhead, p.Stats.FilesChanged,
			p.Stats.Insertions, p.Stats.Deletions)
	}
	return nil
}

// runPRShow prints the diff + commit list for a PR branch.
//
// With no args: do-what-I-mean dispatch based on PR count:
//   0  → "(no PR branches)"
//   1  → show that PR (saves retyping the branch name)
//   N  → fall back to list with a hint to specify <branch>
func runPRShow(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		cfg, err := config.Load(defaultConfigPath())
		if err != nil {
			return err
		}
		siteDir, _ := filepath.Abs(cfg.SiteTemplateDir)
		prs, err := prflow.List(cfg.GitBin, siteDir, "main")
		if err != nil {
			return fmt.Errorf("list PR branches: %w", err)
		}
		switch len(prs) {
		case 0:
			fmt.Println("(no PR branches)")
			return nil
		case 1:
			args = []string{prs[0].Name} // fall through to single-PR show
		default:
			fmt.Fprintln(os.Stderr, "multiple PR branches open; pass one explicitly:")
			fmt.Fprintln(os.Stderr)
			return runPRList(nil, log)
		}
	}
	branch := args[0]
	pr, ok := prflow.ParsePRBranch(branch)
	if !ok {
		return fmt.Errorf("not a PR branch (expected pr/<userid>/<topic>): %s", branch)
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return err
	}
	siteDir, _ := filepath.Abs(cfg.SiteTemplateDir)

	if !prflow.BranchExists(cfg.GitBin, siteDir, branch) {
		return fmt.Errorf("branch does not exist: %s", branch)
	}

	stats, err := prflow.DiffStats(cfg.GitBin, siteDir, branch, "main")
	if err != nil {
		return err
	}
	commits, _ := prflow.CommitLog(cfg.GitBin, siteDir, branch, "main")
	files, _ := prflow.FilesChangedList(cfg.GitBin, siteDir, branch, "main")

	// Resolve author username best-effort.
	author := pr.UserID
	if st, err := store.Open(cfg.DBPath()); err == nil {
		defer st.Close()
		if u, err := st.GetUserByID(context.Background(), pr.UserID); err == nil && u.Username != "" {
			author = u.Username + " (" + pr.UserID + ")"
		}
	}

	fmt.Printf("PR:        %s\n", branch)
	fmt.Printf("Topic:     %s\n", pr.Topic)
	fmt.Printf("Author:    %s\n", author)
	fmt.Printf("Vs main:   %d commits ahead, %d files changed (+%d/-%d)\n",
		stats.CommitsAhead, stats.FilesChanged, stats.Insertions, stats.Deletions)
	fmt.Println()
	if len(commits) > 0 {
		fmt.Println("Commits:")
		for _, c := range commits {
			fmt.Printf("  %s\n", c)
		}
		fmt.Println()
	}
	if len(files) > 0 {
		fmt.Println("Files:")
		for _, f := range files {
			fmt.Printf("  %s\n", f)
		}
		fmt.Println()
	}
	fmt.Printf("merge: homa pr merge %s\n", branch)
	fmt.Printf("close: homa pr close %s\n", branch)
	return nil
}

// runPRMerge merges a PR branch into main. Mirrors the existing
// runMerge mechanic — git merge --no-ff, conflicts surface natively.
// Does NOT auto-commit user worktree state (the PR branch is the
// source of truth; uncommitted state in user/<id>/ worktree is
// unrelated).
func runPRMerge(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: homa pr merge <branch>")
	}
	branch := args[0]
	pr, ok := prflow.ParsePRBranch(branch)
	if !ok {
		return fmt.Errorf("not a PR branch: %s", branch)
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return err
	}
	siteDir, _ := filepath.Abs(cfg.SiteTemplateDir)
	if !prflow.BranchExists(cfg.GitBin, siteDir, branch) {
		return fmt.Errorf("branch does not exist: %s", branch)
	}

	log.Info("pr merge: starting", "branch", branch, "user_id", pr.UserID, "into", "main", "repo", siteDir)
	cmd := exec.Command(cfg.GitBin, "-C", siteDir, "merge", "--no-ff",
		"-m", "homa: merge "+branch, branch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git merge: %w (resolve conflicts in %s, then 'git -C %s commit')",
			err, siteDir, siteDir)
	}
	log.Info("pr merge: success — mainsite vite will HMR", "branch", branch)
	return nil
}

// runPRClose deletes a PR branch without merging.
func runPRClose(args []string, log *slog.Logger) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: homa pr close <branch>")
	}
	branch := args[0]
	if _, ok := prflow.ParsePRBranch(branch); !ok {
		return fmt.Errorf("not a PR branch: %s", branch)
	}
	cfg, err := config.Load(defaultConfigPath())
	if err != nil {
		return err
	}
	siteDir, _ := filepath.Abs(cfg.SiteTemplateDir)
	if !prflow.BranchExists(cfg.GitBin, siteDir, branch) {
		return fmt.Errorf("branch does not exist: %s", branch)
	}
	if err := prflow.DeleteBranch(cfg.GitBin, siteDir, branch); err != nil {
		return fmt.Errorf("delete branch: %w", err)
	}
	log.Info("pr close: deleted", "branch", branch)
	return nil
}

// defaultConfigPath returns the default config.json path used by every
// subcommand + the `-config` flag. Resolution order:
//   1. HOMA_CONFIG env var (absolute path, lets the CLI work from any cwd)
//   2. "config.json" relative to CWD (the original behavior; works when
//      run from ~/homa/ but not from anywhere else)
//
// Symlinked invocation (e.g. ~/.local/bin/homa → ~/homa/homa) needs (1)
// because the working directory is wherever the user typed the command,
// not the directory where the binary lives.
func defaultConfigPath() string {
	if p := os.Getenv("HOMA_CONFIG"); p != "" {
		return p
	}
	return "config.json"
}
