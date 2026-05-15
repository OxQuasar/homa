package provision

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
	"github.com/skipper/homa/orchestrator/internal/tsserve"
	"github.com/skipper/homa/orchestrator/internal/worktree"
)

// UserLookup is the slice of *store.Store that PodmanProvisioner needs for
// EnsureRunning. Narrow interface lets tests pass a fake without importing
// the whole store package's machinery.
type UserLookup interface {
	GetUserByID(ctx context.Context, id string) (*store.User, error)
}

// containerNamePrefix prefixes every per-user container name. Single source
// of truth — sandbox.Manager doesn't care, but the prefix matters for
// `podman ps` / `podman logs` scraping and lets lifecycle.GC scope its
// scans to homa-managed containers.
const containerNamePrefix = "homa-user-"

// Probe is the readiness probe used after the sandbox starts. Returns true
// when the upstream returns a non-5xx response. Defaults to HTTPProbe.
type Probe func(ctx context.Context, url string) (bool, error)

// PodmanProvisioner implements Provisioner with real git + podman + tailscale
// side effects. Tear-down on failure is best-effort, reverse-order, and
// never shadows the root cause.
type PodmanProvisioner struct {
	Worktree          worktree.Service
	Sandbox           sandbox.Manager
	TSServe           tsserve.Service
	Users             UserLookup // needed by EnsureRunning; *store.Store satisfies it
	Ports             *PortAllocator
	Probe             Probe // nil → HTTPProbe
	SiteTemplateDir   string
	BranchesDir       string
	ImageRef          string
	PreviewBaseURL    string // empty disables PreviewURL on Result
	MemoryLimit       string
	CPULimit          string
	AnthropicAPIKey   string // injected into the container as $ANTHROPIC_API_KEY
	ReadinessTimeout  time.Duration
	ReadinessInterval time.Duration
	Log               *slog.Logger
}

// completedSteps records which side-effect steps succeeded so tear-down on
// failure can roll back in the reverse order.
type completedSteps struct {
	worktreeCreated bool
	sandboxStarted  bool
	tsserveRegistered bool
	worktreePath    string
	containerName   string
	servePort       int
	repoDir         string
}

// Provision walks the §9 flow. On any error, runs reverse-order cleanup and
// returns the underlying error (wrapped with `%w`).
func (pp *PodmanProvisioner) Provision(ctx context.Context, userID string) (Result, error) {
	if userID == "" {
		return Result{}, fmt.Errorf("provision: empty userID")
	}
	if pp.Log == nil {
		pp.Log = slog.Default()
	}
	probe := pp.Probe
	if probe == nil {
		probe = HTTPProbe
	}

	nousPort := pp.Ports.NextHostPort()
	previewPort := pp.Ports.NextHostPort()
	servePort := pp.Ports.NextServePort()

	branchName := "user/" + userID
	worktreePath := filepath.Join(pp.BranchesDir, userID)
	containerName := containerNamePrefix + userID

	steps := &completedSteps{
		worktreePath:  worktreePath,
		containerName: containerName,
		servePort:     servePort,
		repoDir:       pp.SiteTemplateDir,
	}

	// 1. git worktree add
	if err := pp.Worktree.Create(ctx, pp.SiteTemplateDir, branchName, worktreePath); err != nil {
		return Result{}, pp.fail(ctx, steps, fmt.Errorf("provision: worktree create: %w", err))
	}
	steps.worktreeCreated = true

	// 2. podman run
	spec := sandbox.Spec{
		ContainerName: containerName,
		ImageRef:      pp.ImageRef,
		WorktreePath:  worktreePath,
		NousPort:      nousPort,
		PreviewPort:   previewPort,
		MemoryLimit:   pp.MemoryLimit,
		CPULimit:      pp.CPULimit,
		Env:           map[string]string{"ANTHROPIC_API_KEY": pp.AnthropicAPIKey},
	}
	if err := pp.Sandbox.Ensure(ctx, spec); err != nil {
		return Result{}, pp.fail(ctx, steps, fmt.Errorf("provision: sandbox ensure: %w", err))
	}
	steps.sandboxStarted = true

	// 3. wait for the Vite dev server inside the container to answer.
	previewURL := fmt.Sprintf("http://127.0.0.1:%d/", previewPort)
	if err := waitReady(ctx, probe, previewURL, pp.ReadinessTimeout, pp.ReadinessInterval); err != nil {
		return Result{}, pp.fail(ctx, steps, fmt.Errorf("provision: readiness: %w", err))
	}

	// 4. tailscale serve --bg --https=<port> http://localhost:<previewPort>
	target := fmt.Sprintf("http://localhost:%d", previewPort)
	if err := pp.TSServe.Register(ctx, servePort, target); err != nil {
		return Result{}, pp.fail(ctx, steps, fmt.Errorf("provision: tsserve register: %w", err))
	}
	steps.tsserveRegistered = true

	result := Result{
		BranchName:       branchName,
		WorktreePath:     worktreePath,
		ContainerName:    containerName,
		NousPort:         nousPort,
		PreviewPort:      previewPort,
		PreviewServePort: servePort,
	}
	if pp.PreviewBaseURL != "" {
		result.PreviewURL = pp.PreviewBaseURL + ":" + strconv.Itoa(servePort)
	}
	return result, nil
}

// fail tears down completed steps in reverse order and returns rootErr
// unchanged (so callers can errors.Is on the root cause).
func (pp *PodmanProvisioner) fail(ctx context.Context, steps *completedSteps, rootErr error) error {
	if steps.tsserveRegistered {
		if err := pp.TSServe.Unregister(ctx, steps.servePort); err != nil {
			pp.Log.Warn("teardown: tsserve unregister failed", "port", steps.servePort, "err", err)
		}
	}
	if steps.sandboxStarted {
		if err := pp.Sandbox.Stop(ctx, steps.containerName); err != nil {
			pp.Log.Warn("teardown: sandbox stop failed", "name", steps.containerName, "err", err)
		}
	}
	if steps.worktreeCreated {
		if err := pp.Worktree.Remove(ctx, steps.repoDir, steps.worktreePath); err != nil {
			pp.Log.Warn("teardown: worktree remove failed", "path", steps.worktreePath, "err", err)
		}
	}
	return rootErr
}

// waitReady polls probe until it returns true, or the deadline lapses.
func waitReady(ctx context.Context, probe Probe, url string, timeout, interval time.Duration) error {
	deadline := time.Now().UTC().Add(timeout)
	for {
		ok, err := probe(ctx, url)
		if ok && err == nil {
			return nil
		}
		if time.Now().UTC().After(deadline) {
			if err != nil {
				return fmt.Errorf("readiness deadline (%s): last err: %w", timeout, err)
			}
			return fmt.Errorf("readiness deadline (%s) reached for %s", timeout, url)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// httpProbeTimeout is the per-attempt HTTP timeout — separate from the
// overall readiness timeout so a hung connection doesn't burn the whole
// budget on one attempt.
const httpProbeTimeout = 2 * time.Second

// HTTPProbe makes a GET request and returns true on any non-5xx response.
// We accept 2xx/3xx/4xx because Vite occasionally emits 404 during its
// initial dependency-optimisation phase but is otherwise serving.
func HTTPProbe(ctx context.Context, url string) (bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, httpProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, nil // network error — keep polling
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500, nil
}

// EnsureRunning brings an existing user's sandbox back up if it stopped
// (idle GC, daemon restart, etc.). Idempotent: no-op when the container is
// already running. Does NOT allocate new ports — the user's row is the
// source of truth.
//
// On failure, returns the error; caller (auth.Login) treats this as
// non-fatal — login still 200s and the editor SPA's WS-status indicator
// will surface the failed dial as a `closed` state. The user can refresh
// to trigger a retry.
func (pp *PodmanProvisioner) EnsureRunning(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("EnsureRunning: empty userID")
	}
	if pp.Users == nil {
		return fmt.Errorf("EnsureRunning: Users lookup not configured")
	}
	if pp.Log == nil {
		pp.Log = slog.Default()
	}

	u, err := pp.Users.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("EnsureRunning: load user %s: %w", userID, err)
	}

	probe := pp.Probe
	if probe == nil {
		probe = HTTPProbe
	}

	spec := sandbox.Spec{
		ContainerName: u.ContainerName,
		ImageRef:      pp.ImageRef,
		WorktreePath:  u.WorktreePath,
		NousPort:      u.NousPort,
		PreviewPort:   u.PreviewPort,
		MemoryLimit:   pp.MemoryLimit,
		CPULimit:      pp.CPULimit,
		Env:           map[string]string{"ANTHROPIC_API_KEY": pp.AnthropicAPIKey},
	}
	if err := pp.Sandbox.Ensure(ctx, spec); err != nil {
		return fmt.Errorf("EnsureRunning: sandbox ensure %s: %w", u.ContainerName, err)
	}

	// Wait for the dev server before re-registering tsserve, otherwise a
	// browser hitting the preview URL right after login can race the boot.
	previewURL := fmt.Sprintf("http://127.0.0.1:%d/", u.PreviewPort)
	if err := waitReady(ctx, probe, previewURL, pp.ReadinessTimeout, pp.ReadinessInterval); err != nil {
		return fmt.Errorf("EnsureRunning: readiness: %w", err)
	}

	// Re-register tsserve if missing (e.g. tailscale was restarted on the
	// host). IsRegistered failure → assume not registered and try anyway.
	registered, err := pp.TSServe.IsRegistered(ctx, u.PreviewServePort)
	if err != nil {
		pp.Log.Warn("EnsureRunning: tsserve IsRegistered failed; will re-register",
			"user_id", userID, "err", err)
		registered = false
	}
	if !registered {
		target := fmt.Sprintf("http://localhost:%d", u.PreviewPort)
		if err := pp.TSServe.Register(ctx, u.PreviewServePort, target); err != nil {
			return fmt.Errorf("EnsureRunning: tsserve register: %w", err)
		}
	}
	return nil
}

// Ensure compile-time interface satisfaction.
var _ Provisioner = (*PodmanProvisioner)(nil)
