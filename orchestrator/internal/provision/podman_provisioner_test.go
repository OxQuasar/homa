package provision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// ----------------------------------------------------------------------------
// Fakes for worktree.Service, sandbox.Manager, tsserve.Service. Each records
// call order in a shared *callLog so tests can assert sequencing.
// ----------------------------------------------------------------------------

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (c *callLog) add(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, name)
}

func (c *callLog) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.calls))
	copy(out, c.calls)
	return out
}

type fakeWorktree struct {
	log       *callLog
	createErr error
	removeErr error
}

func (f *fakeWorktree) Create(_ context.Context, _, _, _ string) error {
	f.log.add("worktree.Create")
	return f.createErr
}
func (f *fakeWorktree) Remove(_ context.Context, _, _ string) error {
	f.log.add("worktree.Remove")
	return f.removeErr
}

type fakeSandbox struct {
	log       *callLog
	ensureErr error
	stopErr   error
	running   bool
	lastSpec  sandbox.Spec // captured for assertion in mount tests
}

func (f *fakeSandbox) Ensure(_ context.Context, spec sandbox.Spec) error {
	f.log.add("sandbox.Ensure")
	f.lastSpec = spec
	return f.ensureErr
}
func (f *fakeSandbox) Stop(_ context.Context, _ string) error {
	f.log.add("sandbox.Stop")
	return f.stopErr
}
func (f *fakeSandbox) IsRunning(_ context.Context, _ string) (bool, error) {
	return f.running, nil
}
func (f *fakeSandbox) Logs(_ context.Context, _ string, _ int) ([]string, error) {
	return nil, nil
}

type fakeTSServe struct {
	log            *callLog
	registerErr    error
	unregisterErr  error
	isRegistered   bool
}

func (f *fakeTSServe) Register(_ context.Context, _ int, _ string) error {
	f.log.add("tsserve.Register")
	return f.registerErr
}
func (f *fakeTSServe) Unregister(_ context.Context, _ int) error {
	f.log.add("tsserve.Unregister")
	return f.unregisterErr
}
func (f *fakeTSServe) IsRegistered(_ context.Context, _ int) (bool, error) {
	return f.isRegistered, nil
}

// ----------------------------------------------------------------------------

func newRig(t *testing.T) (*PodmanProvisioner, *callLog, *fakeWorktree, *fakeSandbox, *fakeTSServe) {
	t.Helper()
	cl := &callLog{}
	wt := &fakeWorktree{log: cl}
	sb := &fakeSandbox{log: cl}
	ts := &fakeTSServe{log: cl}
	pp := &PodmanProvisioner{
		Worktree:          wt,
		Sandbox:           sb,
		TSServe:           ts,
		Ports:             NewPortAllocator(40000, 10001),
		Probe:             func(_ context.Context, _ string) (bool, error) { return true, nil },
		SiteTemplateDir:   "/template",
		BranchesDir:       "/branches",
		ImageRef:          "homa-sandbox:latest",
		PreviewBaseURL:    "https://homa.tailnet.ts.net",
		MemoryLimit:       "2g",
		CPULimit:          "2",
		AnthropicAPIKey:   "test-key",
		ReadinessTimeout:  500 * time.Millisecond,
		ReadinessInterval: 10 * time.Millisecond,
		Log:               slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	return pp, cl, wt, sb, ts
}

// TestProvisionHappyPath — order: worktree → sandbox → (probe) → tsserve;
// Result fields populated correctly.
func TestProvisionHappyPath(t *testing.T) {
	pp, cl, _, _, _ := newRig(t)
	res, err := pp.Provision(context.Background(), "abcd1234")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	wantCalls := []string{"worktree.Create", "sandbox.Ensure", "tsserve.Register"}
	if got := cl.snapshot(); !equalStrings(got, wantCalls) {
		t.Errorf("call order: got %v, want %v", got, wantCalls)
	}

	if res.BranchName != "user/abcd1234" {
		t.Errorf("BranchName: got %q", res.BranchName)
	}
	if res.WorktreePath != "/branches/abcd1234" {
		t.Errorf("WorktreePath: got %q", res.WorktreePath)
	}
	if res.ContainerName != "homa-user-abcd1234" {
		t.Errorf("ContainerName: got %q", res.ContainerName)
	}
	if res.NousPort != 40000 || res.PreviewPort != 40001 || res.PreviewServePort != 10001 {
		t.Errorf("ports: nous=%d preview=%d serve=%d", res.NousPort, res.PreviewPort, res.PreviewServePort)
	}
	if res.PreviewURL != "https://homa.tailnet.ts.net:10001" {
		t.Errorf("PreviewURL: got %q", res.PreviewURL)
	}
}

// TestProvisionWorktreeFail — sandbox.Ensure must NOT be called.
func TestProvisionWorktreeFail(t *testing.T) {
	pp, cl, wt, _, _ := newRig(t)
	wt.createErr = errors.New("git: ref already exists")
	_, err := pp.Provision(context.Background(), "abcd1234")
	if err == nil {
		t.Fatal("Provision: got nil err, want failure")
	}
	for _, c := range cl.snapshot() {
		if c == "sandbox.Ensure" {
			t.Fatalf("unexpected sandbox.Ensure call: %v", cl.snapshot())
		}
	}
}

// TestProvisionSandboxFail — worktree.Remove must be called; tsserve.Register must NOT.
func TestProvisionSandboxFail(t *testing.T) {
	pp, cl, _, sb, _ := newRig(t)
	sb.ensureErr = errors.New("podman: image pull failed")
	_, err := pp.Provision(context.Background(), "abcd1234")
	if err == nil {
		t.Fatal("Provision: got nil err, want failure")
	}
	wantCalls := []string{"worktree.Create", "sandbox.Ensure", "worktree.Remove"}
	if got := cl.snapshot(); !equalStrings(got, wantCalls) {
		t.Errorf("call order: got %v, want %v", got, wantCalls)
	}
}

// TestProvisionReadinessTimeout — sandbox.Stop and worktree.Remove must be called.
func TestProvisionReadinessTimeout(t *testing.T) {
	pp, cl, _, _, _ := newRig(t)
	pp.Probe = func(_ context.Context, _ string) (bool, error) {
		return false, errors.New("connection refused")
	}
	pp.ReadinessTimeout = 50 * time.Millisecond
	pp.ReadinessInterval = 10 * time.Millisecond

	_, err := pp.Provision(context.Background(), "abcd1234")
	if err == nil {
		t.Fatal("Provision: got nil err, want timeout")
	}
	wantCalls := []string{"worktree.Create", "sandbox.Ensure", "sandbox.Stop", "worktree.Remove"}
	if got := cl.snapshot(); !equalStrings(got, wantCalls) {
		t.Errorf("call order: got %v, want %v", got, wantCalls)
	}
}

// TestProvisionTsServeFail — sandbox.Stop + worktree.Remove must be called.
func TestProvisionTsServeFail(t *testing.T) {
	pp, cl, _, _, ts := newRig(t)
	ts.registerErr = errors.New("tailscale: not authorised")
	_, err := pp.Provision(context.Background(), "abcd1234")
	if err == nil {
		t.Fatal("Provision: got nil err")
	}
	wantCalls := []string{"worktree.Create", "sandbox.Ensure", "tsserve.Register", "sandbox.Stop", "worktree.Remove"}
	if got := cl.snapshot(); !equalStrings(got, wantCalls) {
		t.Errorf("call order: got %v, want %v", got, wantCalls)
	}
}

// TestProvisionRootErrorWrappedNotShadowed — even when tear-down errors,
// the returned error matches the original cause via errors.Is.
func TestProvisionRootErrorWrappedNotShadowed(t *testing.T) {
	rootCause := errors.New("podman: image pull failed")
	pp, _, wt, sb, _ := newRig(t)
	sb.ensureErr = rootCause
	// And the teardown itself fails — must not shadow rootCause.
	wt.removeErr = errors.New("git: worktree busy")

	_, err := pp.Provision(context.Background(), "abcd1234")
	if err == nil {
		t.Fatal("Provision: got nil err")
	}
	if !errors.Is(err, rootCause) {
		t.Errorf("errors.Is: got %v, want underlying %v", err, rootCause)
	}
}

// TestProvisionEmptyUserID — short-circuit, no side effects.
func TestProvisionEmptyUserID(t *testing.T) {
	pp, cl, _, _, _ := newRig(t)
	_, err := pp.Provision(context.Background(), "")
	if err == nil {
		t.Fatal("expected err for empty userID")
	}
	if len(cl.snapshot()) != 0 {
		t.Errorf("expected no calls, got %v", cl.snapshot())
	}
}

// TestProvisionClaudeCredsMountWhenFileExists — when ClaudeCredentialsPath
// points at an existing file, the bind mount appears on the Spec passed to
// Sandbox.Ensure. Missing file → no mount; empty path → no mount.
func TestProvisionClaudeCredsMount(t *testing.T) {
	dir := t.TempDir()
	existing := dir + "/.credentials.json"
	if err := os.WriteFile(existing, []byte(`{"claudeAiOauth":{"accessToken":"x"}}`), 0o600); err != nil {
		t.Fatalf("seed creds: %v", err)
	}
	missing := dir + "/does-not-exist.json"

	cases := []struct {
		name      string
		credsPath string
		wantMount bool
	}{
		{"empty path → no mount", "", false},
		{"missing file → no mount", missing, false},
		{"existing file → ro mount", existing, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pp, _, _, sb, _ := newRig(t)
			pp.ClaudeCredentialsPath = tc.credsPath
			if _, err := pp.Provision(context.Background(), "abcd1234"); err != nil {
				t.Fatalf("Provision: %v", err)
			}
			gotMounts := sb.lastSpec.Mounts
			if tc.wantMount {
				if len(gotMounts) != 1 {
					t.Fatalf("mounts: got %v, want 1", gotMounts)
				}
				m := gotMounts[0]
				if m.Src != tc.credsPath {
					t.Errorf("Src: got %q, want %q", m.Src, tc.credsPath)
				}
				if m.Dst != "/root/.claude/.credentials.json" {
					t.Errorf("Dst: got %q", m.Dst)
				}
				if !m.ReadOnly {
					t.Error("ReadOnly: got false, want true")
				}
			} else {
				if len(gotMounts) != 0 {
					t.Errorf("mounts: got %v, want none", gotMounts)
				}
			}
		})
	}
}

// TestEnsureRunningClaudeCredsMount — same mount semantics on the recovery
// path. EnsureRunning is what kicks in after idle-GC, so this is where the
// "host refresh visible to respawned sandbox" benefit actually applies.
func TestEnsureRunningClaudeCredsMount(t *testing.T) {
	dir := t.TempDir()
	existing := dir + "/.credentials.json"
	if err := os.WriteFile(existing, []byte(`{"claudeAiOauth":{"accessToken":"x"}}`), 0o600); err != nil {
		t.Fatalf("seed creds: %v", err)
	}
	pp, _, _, sb, _ := newRig(t)
	pp.ClaudeCredentialsPath = existing
	pp.Users = &fakeUserLookup{users: map[string]*store.User{"abcd1234": sampleUser()}}
	if err := pp.EnsureRunning(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if len(sb.lastSpec.Mounts) != 1 || sb.lastSpec.Mounts[0].Src != existing {
		t.Errorf("EnsureRunning mounts: got %+v, want one with Src=%s", sb.lastSpec.Mounts, existing)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- EnsureRunning tests ---

type fakeUserLookup struct {
	users map[string]*store.User
}

func (f *fakeUserLookup) GetUserByID(_ context.Context, id string) (*store.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func sampleUser() *store.User {
	return &store.User{
		ID:               "abcd1234",
		Email:            "x@x.io",
		ContainerName:    "homa-user-abcd1234",
		WorktreePath:     "/branches/abcd1234",
		NousPort:         40000,
		PreviewPort:      40001,
		PreviewServePort: 10001,
	}
}

// TestEnsureRunningHappyPath — calls Sandbox.Ensure → probe → TSServe
// IsRegistered (false) → TSServe.Register. No port allocation.
func TestEnsureRunningHappyPath(t *testing.T) {
	pp, cl, _, _, _ := newRig(t)
	pp.Users = &fakeUserLookup{users: map[string]*store.User{"abcd1234": sampleUser()}}
	if err := pp.EnsureRunning(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	want := []string{"sandbox.Ensure", "tsserve.Register"}
	if got := cl.snapshot(); !equalStrings(got, want) {
		t.Errorf("call order: got %v, want %v", got, want)
	}
}

// TestEnsureRunningSkipsRegisterWhenAlreadyRegistered — IsRegistered=true →
// no second Register call.
func TestEnsureRunningSkipsRegisterWhenAlreadyRegistered(t *testing.T) {
	pp, cl, _, _, ts := newRig(t)
	ts.isRegistered = true
	pp.Users = &fakeUserLookup{users: map[string]*store.User{"abcd1234": sampleUser()}}
	if err := pp.EnsureRunning(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	for _, c := range cl.snapshot() {
		if c == "tsserve.Register" {
			t.Errorf("unexpected tsserve.Register call: %v", cl.snapshot())
		}
	}
}

// TestEnsureRunningEmptyUserID — short-circuits.
func TestEnsureRunningEmptyUserID(t *testing.T) {
	pp, _, _, _, _ := newRig(t)
	if err := pp.EnsureRunning(context.Background(), ""); err == nil {
		t.Error("expected err for empty userID")
	}
}

// TestEnsureRunningPropagatesEnsureError — sandbox.Ensure fails → bubbles up.
func TestEnsureRunningPropagatesEnsureError(t *testing.T) {
	pp, _, _, sb, _ := newRig(t)
	sb.ensureErr = errors.New("podman: image pull failed")
	pp.Users = &fakeUserLookup{users: map[string]*store.User{"abcd1234": sampleUser()}}
	err := pp.EnsureRunning(context.Background(), "abcd1234")
	if err == nil {
		t.Fatal("expected err")
	}
	if !errors.Is(err, sb.ensureErr) {
		t.Errorf("errors.Is: got %v, want underlying %v", err, sb.ensureErr)
	}
}
