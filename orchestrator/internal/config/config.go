// Package config loads orchestrator configuration from JSON on disk.
// Defaults are filled in so an empty (or missing) config.json yields a
// working development setup.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Defaults applied when the config file is missing or fields are unset.
const (
	defaultListenAddr           = ":8080"
	defaultDataDir              = "data"
	defaultBranchesDir          = "branches"
	defaultSiteTemplateDir      = "site-template"
	defaultImageRef             = "homa-sandbox:latest"
	defaultPodmanBin            = "podman"
	defaultTailscaleBin         = "tailscale"
	defaultGitBin               = "git"
	defaultContainerMemory      = "2g"
	defaultContainerCPUs        = "2"
	// Per-user nous configs live under DataDir/configs/<userid>/config.json
	// and the template is shipped with the sandbox source.
	defaultUserConfigsRel       = "configs"
	defaultNousConfigTemplate   = "sandbox/nous.config.json"
	// Main-site sandbox is exposed on a host port well above the user port
	// pool (40000+) so they can't collide.
	defaultMainSiteHostPort     = 40500
	// 30s was too tight for first-run signups: an empty worktree triggers
	// `npm install` inside the container (≈30–60s depending on network +
	// disk). Subsequent EnsureRunning calls reuse node_modules from the
	// persisted worktree and finish in seconds, so this only stretches the
	// first signup. 120s gives plenty of headroom without making real
	// readiness failures (vite crash, port collision) take too long to
	// surface.
	defaultReadinessTimeoutSec  = 120
	defaultReadinessIntervalMS  = 500
	// Lifecycle thresholds — the compact-then-stop loop. IdleAfter was
	// 30 before the compaction step was added; bumped to 60 so the user
	// has a full hour of message-idleness before their context is
	// compacted out from under them.
	defaultIdleAfterMinutes      = 60
	defaultGCIntervalSeconds     = 60
	defaultIdleWarningSeconds    = 60
	defaultCompactTimeoutSeconds = 90
)

// Config is the orchestrator's runtime configuration.
type Config struct {
	// ListenAddr is the host:port the HTTP server binds to.
	ListenAddr string `json:"listen_addr"`

	// DataDir is the directory holding homa.db and any other persistent state.
	// Relative paths resolve against the orchestrator's working directory.
	DataDir string `json:"data_dir"`

	// BranchesDir is the parent directory under which per-user worktrees live.
	// Both StubProvisioner (string only) and PodmanProvisioner (real git
	// worktree) compose WorktreePath from this base + the user id.
	BranchesDir string `json:"branches_dir"`

	// CookieSecure controls the Secure attribute on the homa_session cookie.
	// Pointer so we can distinguish "unset" (→ default true, prod-safe) from
	// "explicitly false" (local HTTP development / tests).
	CookieSecure *bool `json:"cookie_secure"`

	// PreviewBaseURL is the base URL (scheme + host) of the user-preview
	// iframe served via Tailscale Serve, e.g. "https://homa.tailnet.ts.net".
	// Empty disables the /me preview_url field. Final URL is
	// "<base>:<preview_serve_port>".
	PreviewBaseURL string `json:"preview_base_url"`

	// ProvisionHostPortStart overrides the StubProvisioner's host-port
	// counter starting value. 0 → package default (provision.HostPortStart).
	// Used by the e2e script so the first signup's nous_port lands on a
	// known fake-upstream listener.
	ProvisionHostPortStart int `json:"provision_host_port_start"`

	// ProvisionServePortStart overrides the StubProvisioner's tailscale-serve
	// counter starting value. 0 → package default.
	ProvisionServePortStart int `json:"provision_serve_port_start"`

	// --- Provisioner / sandbox runtime -----------------------------------
	// UsePodman selects PodmanProvisioner over StubProvisioner. Default false;
	// flip to true once `homa-sandbox:latest` image is built (see
	// ~/homa/RUNTIME.md). Stub remains the path used by tests + the local
	// e2e script.
	UsePodman bool `json:"use_podman"`

	// SiteTemplateDir is the git repo whose `main` branch every user forks
	// from. Default: "site-template" (resolved relative to the orchestrator
	// CWD, like BranchesDir).
	SiteTemplateDir string `json:"site_template_dir"`

	// ImageRef is the podman image tag for per-user sandboxes.
	ImageRef string `json:"image_ref"`

	// PodmanBin, TailscaleBin, GitBin override the binary names if not on PATH.
	PodmanBin    string `json:"podman_bin"`
	TailscaleBin string `json:"tailscale_bin"`
	GitBin       string `json:"git_bin"`

	// ContainerMemory and ContainerCPUs become `--memory=` and `--cpus=` on
	// `podman run`. Stringly-typed for direct passthrough.
	ContainerMemory string `json:"container_memory"`
	ContainerCPUs   string `json:"container_cpus"`

	// ReadinessTimeoutSec / ReadinessIntervalMS control the post-Ensure
	// dev-server-up probe. 0 → package defaults.
	ReadinessTimeoutSec int `json:"readiness_timeout_sec"`
	ReadinessIntervalMS int `json:"readiness_interval_ms"`

	// AnthropicAPIKey is the value passed as -e ANTHROPIC_API_KEY=... into
	// every sandbox. `$VAR` and `${VAR}` references are expanded at startup
	// via config.ExpandSecret (see cmd/homa/main.go).
	AnthropicAPIKey string `json:"anthropic_api_key"`

	// ClaudeCredentialsPath is an absolute host path to a Claude Code
	// `.credentials.json`. Empty (default) → main.go resolves to
	// `$HOME/.claude/.credentials.json`. When the file exists at
	// provision time the orchestrator bind-mounts it read-only into the
	// sandbox so nous-in-sandbox uses the OAuth chain (and picks up host
	// token refreshes for free). Set to "-" to disable explicitly.
	ClaudeCredentialsPath string `json:"claude_credentials_path"`

	// UserConfigsDir is the host directory holding per-user nous configs.
	// Path layout: <UserConfigsDir>/<userid>/config.json. Each file is
	// bind-mounted read-only into its sandbox at /usr/local/bin/config.json,
	// shadowing the image-baked default. New users get the file seeded
	// from NousConfigTemplate at signup. Admin-controlled — users can't
	// edit (RO mount), only operators with host access can. Empty
	// disables per-user configs entirely (falls back to baked image).
	// Default: "configs" (resolved relative to DataDir → absolute path).
	UserConfigsDir string `json:"user_configs_dir"`

	// NousConfigTemplate is the path to the per-user nous config template
	// copied to <UserConfigsDir>/<userid>/config.json on first provision.
	// Default: "sandbox/nous.config.json" (resolved relative to CWD).
	NousConfigTemplate string `json:"nous_config_template"`

	// MainSiteEnabled gates the orchestrator-owned public-site sandbox.
	// When true, a singleton container ("homa-main") runs site-template/
	// and the reverse proxy forwards / and unmatched paths to it.
	// When false, falls back to the old behavior: GET / serves the SPA
	// login page (set by the SPA-side router from the empty path).
	// Default: true when UsePodman is true, false otherwise.
	MainSiteEnabled *bool `json:"main_site_enabled"`

	// MainSiteHostPort is the host port the main sandbox's vite is exposed on.
	// Reverse proxy forwards / to 127.0.0.1:<this>. Separate from the user
	// port pool (40000+) so they can't collide. Default: 40500.
	MainSiteHostPort int `json:"main_site_host_port"`

	// IdleAfterMinutes is the inactivity window before the lifecycle
	// compacts the user's nous context and stops their sandbox. Measured
	// against users.last_message_at (NOT the WS keepalive ticker), so a
	// "tab left open with no messages" rolls into the cold-and-compacted
	// state. Unset / 0 in JSON → default 60. Negative disables the
	// lifecycle entirely.
	IdleAfterMinutes int `json:"idle_after_minutes"`

	// GCIntervalSeconds is how often the lifecycle loop ticks. Same
	// defaulting semantics as IdleAfterMinutes — negative disables,
	// 0 → default 60.
	GCIntervalSeconds int `json:"gc_interval_seconds"`

	// IdleWarningSeconds is the lead time before idle compaction during
	// which the orchestrator pushes a homa.idle_warning frame to the
	// browser so the editor can surface a banner. 0 → default 60.
	IdleWarningSeconds int `json:"idle_warning_seconds"`

	// CompactTimeoutSeconds bounds how long the lifecycle waits for the
	// full_compact round-trip against the user's nous before giving up
	// and proceeding to Stop. 0 → default 90.
	CompactTimeoutSeconds int `json:"compact_timeout_seconds"`
}

// Load reads the config from path. If path is empty or missing, returns a
// fully-defaulted config.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parsing %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// fall through to defaults
		default:
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
	}
	applyDefaults(cfg)
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = defaultListenAddr
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir
	}
	if cfg.BranchesDir == "" {
		cfg.BranchesDir = defaultBranchesDir
	}
	if cfg.CookieSecure == nil {
		t := true
		cfg.CookieSecure = &t
	}
	if cfg.SiteTemplateDir == "" {
		cfg.SiteTemplateDir = defaultSiteTemplateDir
	}
	if cfg.ImageRef == "" {
		cfg.ImageRef = defaultImageRef
	}
	if cfg.PodmanBin == "" {
		cfg.PodmanBin = defaultPodmanBin
	}
	if cfg.TailscaleBin == "" {
		cfg.TailscaleBin = defaultTailscaleBin
	}
	if cfg.GitBin == "" {
		cfg.GitBin = defaultGitBin
	}
	if cfg.ContainerMemory == "" {
		cfg.ContainerMemory = defaultContainerMemory
	}
	if cfg.ContainerCPUs == "" {
		cfg.ContainerCPUs = defaultContainerCPUs
	}
	if cfg.ReadinessTimeoutSec == 0 {
		cfg.ReadinessTimeoutSec = defaultReadinessTimeoutSec
	}
	if cfg.ReadinessIntervalMS == 0 {
		cfg.ReadinessIntervalMS = defaultReadinessIntervalMS
	}
	// IdleAfterMinutes / GCIntervalSeconds: 0 (or absent in JSON) → apply
	// the default; negative is the operator's explicit "disable" signal
	// (kept as-is; main.go's `> 0` gate skips starting the GC). Field
	// doc-comments at lines 99-110.
	if cfg.IdleAfterMinutes == 0 {
		cfg.IdleAfterMinutes = defaultIdleAfterMinutes
	}
	if cfg.GCIntervalSeconds == 0 {
		cfg.GCIntervalSeconds = defaultGCIntervalSeconds
	}
	if cfg.IdleWarningSeconds == 0 {
		cfg.IdleWarningSeconds = defaultIdleWarningSeconds
	}
	if cfg.CompactTimeoutSeconds == 0 {
		cfg.CompactTimeoutSeconds = defaultCompactTimeoutSeconds
	}
	// UserConfigsDir defaults to "configs" *under DataDir* (so it ends up
	// at e.g. data/configs/). Resolved to absolute in main.go.
	if cfg.UserConfigsDir == "" {
		cfg.UserConfigsDir = filepath.Join(cfg.DataDir, defaultUserConfigsRel)
	}
	if cfg.NousConfigTemplate == "" {
		cfg.NousConfigTemplate = defaultNousConfigTemplate
	}
	// MainSite — default enabled when podman is on; off otherwise (the
	// stub provisioner path has no image to run main from).
	if cfg.MainSiteEnabled == nil {
		v := cfg.UsePodman
		cfg.MainSiteEnabled = &v
	}
	if cfg.MainSiteHostPort == 0 {
		cfg.MainSiteHostPort = defaultMainSiteHostPort
	}
}

// DBPath returns the absolute path to homa.db inside DataDir.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "homa.db")
}

// MainSiteOn returns the effective MainSiteEnabled value. Pointer-bool
// indirection mirrors CookieSecure: unset → default (true with podman).
func (c *Config) MainSiteOn() bool { return c.MainSiteEnabled != nil && *c.MainSiteEnabled }

// SecureCookies returns the effective Secure attribute value.
func (c *Config) SecureCookies() bool { return c.CookieSecure != nil && *c.CookieSecure }
