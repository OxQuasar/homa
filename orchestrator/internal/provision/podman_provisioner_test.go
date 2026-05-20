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

// findMount returns the mount whose Dst matches, or nil. Spec mounts have
// no guaranteed slice order, so tests should look up by destination rather
// than positional index.
func findMount(ms []sandbox.Mount, dst string) *sandbox.Mount {
	for i := range ms {
		if ms[i].Dst == dst {
			return &ms[i]
		}
	}
	return nil
}

// TestProvisionNousDataVolumeAlwaysMounted — the nous data dir gets a
// per-user named volume on every Provision, regardless of creds config.
// This is what keeps chat history across container --rm.
func TestProvisionNousDataVolumeAlwaysMounted(t *testing.T) {
	pp, _, _, sb, _ := newRig(t)
	if _, err := pp.Provision(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	m := findMount(sb.lastSpec.Mounts, "/root/.nous")
	if m == nil {
		t.Fatalf("no /root/.nous mount on spec; got %+v", sb.lastSpec.Mounts)
	}
	if m.Src != "homa-user-abcd1234-nous" {
		t.Errorf("volume name: got %q, want %q", m.Src, "homa-user-abcd1234-nous")
	}
	if m.ReadOnly {
		t.Error("nous data volume must be writable")
	}
}

// TestProvisionClaudeCredsMount — the claude credentials bind shows up
// only when configured + file exists. Independent of the nous-data volume,
// which is always present.
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
		wantCreds bool
	}{
		{"empty path → no creds mount", "", false},
		{"missing file → no creds mount", missing, false},
		{"existing file → ro creds mount", existing, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pp, _, _, sb, _ := newRig(t)
			pp.ClaudeCredentialsPath = tc.credsPath
			if _, err := pp.Provision(context.Background(), "abcd1234"); err != nil {
				t.Fatalf("Provision: %v", err)
			}
			creds := findMount(sb.lastSpec.Mounts, "/root/.claude/.credentials.json")
			if tc.wantCreds {
				if creds == nil {
					t.Fatalf("no creds mount; got %+v", sb.lastSpec.Mounts)
				}
				if creds.Src != tc.credsPath {
					t.Errorf("Src: got %q, want %q", creds.Src, tc.credsPath)
				}
				if !creds.ReadOnly {
					t.Error("creds mount must be ReadOnly")
				}
			} else if creds != nil {
				t.Errorf("unexpected creds mount: %+v", *creds)
			}
		})
	}
}

// TestEnsureRunningExtraMounts — recovery path emits the same mount set
// (nous-data volume always; creds bind when configured). EnsureRunning is
// what fires after idle-GC, which is exactly where the volume earns its
// keep — history must survive the respawn.
func TestEnsureRunningExtraMounts(t *testing.T) {
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
	if findMount(sb.lastSpec.Mounts, "/root/.nous") == nil {
		t.Errorf("EnsureRunning missing /root/.nous volume: %+v", sb.lastSpec.Mounts)
	}
	if findMount(sb.lastSpec.Mounts, "/root/.claude/.credentials.json") == nil {
		t.Errorf("EnsureRunning missing creds mount: %+v", sb.lastSpec.Mounts)
	}
}

// TestUserConfigSeedAndMount — first provision creates
// <UserConfigsDir>/<id>/config.json from template; subsequent calls
// reuse the existing file (don't overwrite); the file is bind-mounted
// read-only at /usr/local/bin/config.json on every spec.
func TestUserConfigSeedAndMount(t *testing.T) {
	dir := t.TempDir()
	tmpl := dir + "/tmpl.json"
	templateBody := []byte(`{"providers":{"anthropic":{"default_model":"opus"}}}`)
	if err := os.WriteFile(tmpl, templateBody, 0o644); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	pp, _, _, sb, _ := newRig(t)
	pp.UserConfigsDir = dir + "/configs"
	pp.NousConfigTemplate = tmpl

	// First provision → file gets created, mount present.
	if _, err := pp.Provision(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	want := pp.UserConfigsDir + "/abcd1234/config.json"
	m := findMount(sb.lastSpec.Mounts, "/usr/local/bin/config.json")
	if m == nil {
		t.Fatalf("user config mount missing: %+v", sb.lastSpec.Mounts)
	}
	if m.Src != want {
		t.Errorf("mount Src: got %q, want %q", m.Src, want)
	}
	if !m.ReadOnly {
		t.Error("user config mount must be ReadOnly")
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("seeded file unreadable: %v", err)
	}
	if string(got) != string(templateBody) {
		t.Errorf("seeded contents: got %s, want %s", got, templateBody)
	}

	// Admin edit: change the file. Subsequent EnsureRunning must NOT
	// overwrite — the file is the source of truth once it exists.
	const admin = `{"providers":{"anthropic":{"default_model":"haiku"}}}`
	if err := os.WriteFile(want, []byte(admin), 0o644); err != nil {
		t.Fatalf("admin edit: %v", err)
	}
	pp.Users = &fakeUserLookup{users: map[string]*store.User{"abcd1234": sampleUser()}}
	if err := pp.EnsureRunning(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	got2, _ := os.ReadFile(want)
	if string(got2) != admin {
		t.Errorf("admin edit clobbered: got %s, want %s", got2, admin)
	}
	m2 := findMount(sb.lastSpec.Mounts, "/usr/local/bin/config.json")
	if m2 == nil || m2.Src != want {
		t.Errorf("EnsureRunning lost user config mount: %+v", sb.lastSpec.Mounts)
	}
}

// TestUserConfigDisabled — empty UserConfigsDir → no mount, image-baked
// default applies. Existing fields (nous data volume) still present.
func TestUserConfigDisabled(t *testing.T) {
	pp, _, _, sb, _ := newRig(t)
	pp.UserConfigsDir = ""
	if _, err := pp.Provision(context.Background(), "abcd1234"); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if findMount(sb.lastSpec.Mounts, "/usr/local/bin/config.json") != nil {
		t.Errorf("user config mount present despite UserConfigsDir=\"\": %+v", sb.lastSpec.Mounts)
	}
	if findMount(sb.lastSpec.Mounts, "/root/.nous") == nil {
		t.Errorf("nous data volume should still be mounted: %+v", sb.lastSpec.Mounts)
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

// TestGitIdentityEnvInjected — verifies the provisioner injects per-user
// GIT_AUTHOR_* / GIT_COMMITTER_* env vars from the user record. Prevents
// the regression where the LLM, finding no git identity in the container,
// wrote user.email/name to the SHARED site-template/.git/config —
// contaminating every other user.
func TestGitIdentityEnvInjected(t *testing.T) {
	pp, _, _, sb, _ := newRig(t)
	u := &store.User{
		ID:               "abcd1234",
		Email:            "alice@example.com",
		Name:             "Alice Liddell",
		Username:         "alice",
		ContainerName:    "homa-user-abcd1234",
		WorktreePath:     "/branches/abcd1234",
		NousPort:         40000,
		PreviewPort:      40001,
		PreviewServePort: 10001,
	}
	pp.Users = &fakeUserLookup{users: map[string]*store.User{u.ID: u}}
	if err := pp.EnsureRunning(context.Background(), u.ID); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if sb.lastSpec.Env["GIT_AUTHOR_NAME"] != "alice" {
		t.Errorf("GIT_AUTHOR_NAME: got %q, want alice (= username)", sb.lastSpec.Env["GIT_AUTHOR_NAME"])
	}
	if sb.lastSpec.Env["GIT_AUTHOR_EMAIL"] != "alice@example.com" {
		t.Errorf("GIT_AUTHOR_EMAIL: got %q", sb.lastSpec.Env["GIT_AUTHOR_EMAIL"])
	}
	if sb.lastSpec.Env["GIT_COMMITTER_NAME"] != "alice" {
		t.Errorf("GIT_COMMITTER_NAME: got %q", sb.lastSpec.Env["GIT_COMMITTER_NAME"])
	}
	if sb.lastSpec.Env["GIT_COMMITTER_EMAIL"] != "alice@example.com" {
		t.Errorf("GIT_COMMITTER_EMAIL: got %q", sb.lastSpec.Env["GIT_COMMITTER_EMAIL"])
	}
}

// TestGitIdentityFallsBackWhenUsernameMissing — legacy users with empty
// username + name fall back to userID; empty email falls back to
// <name>@homa.local so git doesn't crash on commit.
func TestGitIdentityFallsBackWhenUsernameMissing(t *testing.T) {
	pp, _, _, sb, _ := newRig(t)
	u := &store.User{
		ID:               "deadbeef",
		Email:            "",
		Name:             "",
		Username:         "",
		ContainerName:    "homa-user-deadbeef",
		WorktreePath:     "/branches/deadbeef",
		NousPort:         40010,
		PreviewPort:      40011,
		PreviewServePort: 10010,
	}
	pp.Users = &fakeUserLookup{users: map[string]*store.User{u.ID: u}}
	if err := pp.EnsureRunning(context.Background(), u.ID); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if sb.lastSpec.Env["GIT_AUTHOR_NAME"] != "deadbeef" {
		t.Errorf("fallback to userID: got %q", sb.lastSpec.Env["GIT_AUTHOR_NAME"])
	}
	if sb.lastSpec.Env["GIT_AUTHOR_EMAIL"] != "deadbeef@homa.local" {
		t.Errorf("synthetic email: got %q", sb.lastSpec.Env["GIT_AUTHOR_EMAIL"])
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
