package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// testEnv bundles everything a handler test needs.
type testEnv struct {
	t      *testing.T
	srv    *httptest.Server
	store  *store.Store
	client *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	prov := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	// secureCookies=false because httptest serves plain HTTP — production
	// instances pass true (see config.Config.CookieSecure default).
	svc := auth.New(st, prov, false, "", nil, log)

	mux := http.NewServeMux()
	svc.Register(mux, nil)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	return &testEnv{t: t, srv: srv, store: st, client: client}
}

func (e *testEnv) post(path string, body any) *http.Response {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			e.t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, &buf)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("do: %v", err)
	}
	return resp
}

func (e *testEnv) get(path string) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("do: %v", err)
	}
	return resp
}

// cookieFromResp extracts the homa_session cookie from a response, or nil if
// no such cookie was set.
func cookieFromResp(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// --- tests ---

func TestSignupHappyPath(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/signup", map[string]string{
		"email": "a@b.co", "password": "hunter22", "name": "A", "username": "alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var got struct{ UserID string `json:"user_id"` }
	decodeBody(t, resp, &got)
	if got.UserID == "" {
		t.Fatal("empty user_id")
	}

	// Cookie checks
	c := cookieFromResp(resp)
	if c == nil {
		t.Fatal("no homa_session cookie set")
	}
	if !c.HttpOnly {
		t.Error("cookie missing HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: got %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path: got %q, want /", c.Path)
	}
	wantMaxAge := int((30 * 24 * time.Hour).Seconds())
	if c.MaxAge != wantMaxAge {
		t.Errorf("MaxAge: got %d, want %d", c.MaxAge, wantMaxAge)
	}

	// DB row checks
	ctx := context.Background()
	u, err := env.store.GetUserByID(ctx, got.UserID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u.Email != "a@b.co" {
		t.Errorf("email: got %q", u.Email)
	}
	// Password is hashed (not plaintext)
	if u.PasswordHash == "hunter22" {
		t.Fatal("password stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("hunter22")); err != nil {
		t.Errorf("bcrypt verify: %v", err)
	}
	// Provisioned fields populated
	if u.BranchName == "" || u.WorktreePath == "" || u.ContainerName == "" {
		t.Errorf("provisioned strings empty: %+v", u)
	}
	if u.NousPort == 0 || u.PreviewPort == 0 || u.PreviewServePort == 0 {
		t.Errorf("provisioned ports zero: %+v", u)
	}
	if u.BranchName != "user/"+got.UserID {
		t.Errorf("branch_name: got %q, want %q", u.BranchName, "user/"+got.UserID)
	}
	if u.ContainerName != "homa-user-"+got.UserID {
		t.Errorf("container_name: got %q, want %q", u.ContainerName, "homa-user-"+got.UserID)
	}
	// Timestamps set
	now := time.Now().UTC().Unix()
	if u.CreatedAt == 0 || u.CreatedAt > now {
		t.Errorf("created_at unset or future: %d (now=%d)", u.CreatedAt, now)
	}
	if u.LastActiveAt == 0 || u.LastActiveAt > now {
		t.Errorf("last_active_at unset or future: %d", u.LastActiveAt)
	}
}

func TestSignupShortPassword(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/signup", map[string]string{
		"email": "a@b.co", "password": "short",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestSignupBadEmail(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/signup", map[string]string{
		"email": "not-an-email", "password": "hunter22",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestSignupDuplicateEmail(t *testing.T) {
	env := newTestEnv(t)
	first := env.post("/signup", map[string]string{
		"email": "dup@b.co", "password": "hunter22", "username": "dup1",
	})
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first signup: got %d, want 200", first.StatusCode)
	}

	second := env.post("/signup", map[string]string{
		"email": "dup@b.co", "password": "another1", "username": "dup2",
	})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second signup: got %d, want 409", second.StatusCode)
	}
}

func TestLoginHappyPath(t *testing.T) {
	env := newTestEnv(t)
	// First signup
	resp := env.post("/signup", map[string]string{
		"email": "li@b.co", "password": "hunter22", "username": "liuser",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signup: got %d", resp.StatusCode)
	}
	// Drop cookies so login issues a fresh one
	env.client.Jar, _ = cookiejar.New(nil)

	loginResp := env.post("/login", map[string]string{
		"email": "li@b.co", "password": "hunter22",
	})
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login: got %d, want 200", loginResp.StatusCode)
	}
	c := cookieFromResp(loginResp)
	if c == nil {
		t.Fatal("login: no cookie")
	}

	// web_sessions row exists for this token
	if _, err := env.store.GetWebSession(context.Background(), c.Value); err != nil {
		t.Errorf("web_session not in DB: %v", err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	env := newTestEnv(t)
	env.post("/signup", map[string]string{"email": "wp@b.co", "password": "hunter22", "username": "wpuser"}).Body.Close()
	env.client.Jar, _ = cookiejar.New(nil)

	resp := env.post("/login", map[string]string{"email": "wp@b.co", "password": "wrongone"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", resp.StatusCode)
	}
	if cookieFromResp(resp) != nil {
		t.Error("cookie set on failed login")
	}
}

func TestMeWithCookie(t *testing.T) {
	env := newTestEnv(t)
	signup := env.post("/signup", map[string]string{"email": "me@b.co", "password": "hunter22", "username": "meuser"})
	var signupBody struct{ UserID string `json:"user_id"` }
	decodeBody(t, signup, &signupBody)

	resp := env.get("/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var got struct {
		UserID        string `json:"user_id"`
		Email         string `json:"email"`
		PreviewURL    string `json:"preview_url"`
		NousSessionID string `json:"nous_session_id"`
	}
	decodeBody(t, resp, &got)
	if got.UserID != signupBody.UserID {
		t.Errorf("user_id: got %q, want %q", got.UserID, signupBody.UserID)
	}
	// nous_session_id must be populated at signup (8 hex chars matching the
	// nous-side uuid[:8] convention) so the editor's WS Hello can pin a
	// specific session id.
	const wantNousSessionIDLen = 8
	if len(got.NousSessionID) != wantNousSessionIDLen {
		t.Errorf("nous_session_id: got %q (len %d), want %d hex chars",
			got.NousSessionID, len(got.NousSessionID), wantNousSessionIDLen)
	}
	if got.Email != "me@b.co" {
		t.Errorf("email: got %q", got.Email)
	}
	// preview_url is intentionally empty here — auth.New was constructed
	// with previewBaseURL="" so /me has nothing to format.
	if got.PreviewURL != "" {
		t.Errorf("preview_url: got %q, want empty", got.PreviewURL)
	}
}

func TestMeWithoutCookie(t *testing.T) {
	env := newTestEnv(t)
	resp := env.get("/me")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", resp.StatusCode)
	}
}

func TestLogoutAndMeAfterLogout(t *testing.T) {
	env := newTestEnv(t)
	signup := env.post("/signup", map[string]string{"email": "lo@b.co", "password": "hunter22", "username": "louser"})
	signup.Body.Close()

	// Grab the cookie value before logout (jar will be modified)
	u, _ := url.Parse(env.srv.URL)
	cookies := env.client.Jar.Cookies(u)
	var tokenBefore string
	for _, c := range cookies {
		if c.Name == auth.CookieName {
			tokenBefore = c.Value
		}
	}
	if tokenBefore == "" {
		t.Fatal("no session token after signup")
	}

	resp := env.post("/logout", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: got %d, want 204", resp.StatusCode)
	}
	cleared := cookieFromResp(resp)
	if cleared == nil {
		t.Fatal("logout did not return a cookie")
	}
	// Cleared cookie must be invalidating: Max-Age <= 0 or expired
	if cleared.MaxAge > 0 {
		t.Errorf("cleared cookie still valid: MaxAge=%d", cleared.MaxAge)
	}

	// web_sessions row removed
	if _, err := env.store.GetWebSession(context.Background(), tokenBefore); err == nil {
		t.Error("web_session still present after logout")
	}

	// Forge a request that reuses the old token: jar may have cleared the
	// cookie locally, so set it explicitly.
	req, _ := http.NewRequest(http.MethodGet, env.srv.URL+"/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tokenBefore})
	// Use a fresh client (without jar) so the explicit cookie is sent verbatim.
	cleanClient := &http.Client{Timeout: 5 * time.Second}
	meResp, err := cleanClient.Do(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if meResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("me after logout: got %d, want 401", meResp.StatusCode)
	}
	meResp.Body.Close()
}

// TestErrorBodyShape verifies error responses are JSON {"error": ...} per §6.
func TestErrorBodyShape(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/signup", map[string]string{"email": "x", "password": "x"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		t.Errorf("error body not JSON: %s", body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["error"] == "" {
		t.Errorf("error field missing or empty: %v", got)
	}
}

// TestPortAllocationDistinct guards the multi-user invariant: two concurrent
// signups must receive distinct nous/preview/preview_serve ports.
func TestPortAllocationDistinct(t *testing.T) {
	env := newTestEnv(t)
	const n = 4
	type result struct{ id string }
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			c, _ := cookiejar.New(nil)
			client := &http.Client{Jar: c, Timeout: 5 * time.Second}
			body, _ := json.Marshal(map[string]string{
				"email":    fmt.Sprintf("u%d@b.co", i),
				"password": "hunter22",
				"username": fmt.Sprintf("user_%d", i),
			})
			req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/signup", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("signup %d: %v", i, err)
				results <- result{}
				return
			}
			var got struct{ UserID string `json:"user_id"` }
			json.NewDecoder(resp.Body).Decode(&got)
			resp.Body.Close()
			results <- result{id: got.UserID}
		}(i)
	}

	seenNous := map[int]bool{}
	seenPreview := map[int]bool{}
	seenServe := map[int]bool{}
	for i := 0; i < n; i++ {
		r := <-results
		if r.id == "" {
			t.Fatalf("signup %d produced empty id", i)
		}
		u, err := env.store.GetUserByID(context.Background(), r.id)
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if seenNous[u.NousPort] {
			t.Errorf("duplicate nous_port: %d", u.NousPort)
		}
		if seenPreview[u.PreviewPort] {
			t.Errorf("duplicate preview_port: %d", u.PreviewPort)
		}
		if seenServe[u.PreviewServePort] {
			t.Errorf("duplicate preview_serve_port: %d", u.PreviewServePort)
		}
		seenNous[u.NousPort] = true
		seenPreview[u.PreviewPort] = true
		seenServe[u.PreviewServePort] = true
		if u.NousPort == u.PreviewPort {
			t.Errorf("nous_port == preview_port for %s: %d", r.id, u.NousPort)
		}
	}
}

// Sanity: ensure the example config file exists alongside the repo root.
// Helps catch accidental deletion during refactors.
func TestExampleConfigExists(t *testing.T) {
	// orchestrator/internal/auth → orchestrator → homa
	cwd, _ := os.Getwd()
	p := filepath.Join(cwd, "..", "..", "..", "config.json.example")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("config.json.example not found at %s: %v", p, err)
	}
}

// --- Provisioner-interaction tests ---

// countingProvisioner spies on Provisioner.Provision calls so tests can
// verify the auth precheck short-circuits before reaching it.
type countingProvisioner struct {
	delegate provision.Provisioner
	calls    int
}

func (c *countingProvisioner) Provision(ctx context.Context, userID string) (provision.Result, error) {
	c.calls++
	return c.delegate.Provision(ctx, userID)
}
func (c *countingProvisioner) EnsureRunning(ctx context.Context, userID string) error {
	return c.delegate.EnsureRunning(ctx, userID)
}

// TestSignupDuplicateEmailDoesNotInvokeProvisioner — the precheck must
// short-circuit before Provisioner.Provision (otherwise the dup-email path
// runs git/podman/tailscale side effects we then have to roll back).
func TestSignupDuplicateEmailDoesNotInvokeProvisioner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	stub := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))
	spy := &countingProvisioner{delegate: stub}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := auth.New(st, spy, false, "", nil, log)

	mux := http.NewServeMux()
	svc.Register(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	post := func(body any) *http.Response {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		resp, err := client.Post(srv.URL+"/signup", "application/json", &buf)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		return resp
	}

	// First signup — happy path; Provision must run.
	r1 := post(map[string]string{"email": "dup-noprov@x.io", "password": "hunter22", "username": "dupnp1"})
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first signup: %d", r1.StatusCode)
	}
	if spy.calls != 1 {
		t.Fatalf("after first signup: spy.calls=%d, want 1", spy.calls)
	}

	// Drop cookies so the second client doesn't carry over auth — and
	// retry with the same email.
	client.Jar, _ = cookiejar.New(nil)
	r2 := post(map[string]string{"email": "dup-noprov@x.io", "password": "another1", "username": "dupnp2"})
	r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Fatalf("second signup: got %d, want 409", r2.StatusCode)
	}
	if spy.calls != 1 {
		t.Errorf("after dup signup: spy.calls=%d, want still 1 (precheck must short-circuit)", spy.calls)
	}
}

// countingProvisioner2 also counts EnsureRunning so the login-Ensures test
// can distinguish Provision vs EnsureRunning calls.
type countingProvisioner2 struct {
	delegate          provision.Provisioner
	provisionCalls    int
	ensureRunningCalls int
}

func (c *countingProvisioner2) Provision(ctx context.Context, userID string) (provision.Result, error) {
	c.provisionCalls++
	return c.delegate.Provision(ctx, userID)
}
func (c *countingProvisioner2) EnsureRunning(ctx context.Context, userID string) error {
	c.ensureRunningCalls++
	return c.delegate.EnsureRunning(ctx, userID)
}

// TestLoginEnsuresSandbox — signup runs Provision once. Login (and only
// Login) runs EnsureRunning. Second login bumps EnsureRunning again.
func TestLoginEnsuresSandbox(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	stub := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))
	spy := &countingProvisioner2{delegate: stub}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := auth.New(st, spy, false, "", nil, log)

	mux := http.NewServeMux()
	svc.Register(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	post := func(path string, body any) *http.Response {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		resp, err := client.Post(srv.URL+path, "application/json", &buf)
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		return resp
	}

	// 1. Signup → Provision=1, EnsureRunning=0
	r := post("/signup", map[string]string{"email": "le@x.io", "password": "hunter22", "username": "leuser"})
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("signup: %d", r.StatusCode)
	}
	if spy.provisionCalls != 1 || spy.ensureRunningCalls != 0 {
		t.Fatalf("after signup: provision=%d ensure=%d, want 1,0", spy.provisionCalls, spy.ensureRunningCalls)
	}

	// Drop signup cookie so Login sees no prior auth.
	client.Jar, _ = cookiejar.New(nil)

	// 2. First login → EnsureRunning=1
	r = post("/login", map[string]string{"email": "le@x.io", "password": "hunter22"})
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("login 1: %d", r.StatusCode)
	}
	if spy.provisionCalls != 1 || spy.ensureRunningCalls != 1 {
		t.Fatalf("after login 1: provision=%d ensure=%d, want 1,1", spy.provisionCalls, spy.ensureRunningCalls)
	}

	// 3. Second login → EnsureRunning=2
	client.Jar, _ = cookiejar.New(nil)
	r = post("/login", map[string]string{"email": "le@x.io", "password": "hunter22"})
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("login 2: %d", r.StatusCode)
	}
	if spy.provisionCalls != 1 || spy.ensureRunningCalls != 2 {
		t.Fatalf("after login 2: provision=%d ensure=%d, want 1,2", spy.provisionCalls, spy.ensureRunningCalls)
	}
}

// observingProvisioner records, at the moment EnsureRunning is entered,
// the user's persisted LastActiveAt. Used by TestLoginBumpsActiveBeforeEnsure
// to assert the bump happens BEFORE Ensure starts (Login/GC race fix).
type observingProvisioner struct {
	delegate           provision.Provisioner
	store              *store.Store
	lastActiveAtAtEnsure int64
}

func (o *observingProvisioner) Provision(ctx context.Context, userID string) (provision.Result, error) {
	return o.delegate.Provision(ctx, userID)
}
func (o *observingProvisioner) EnsureRunning(ctx context.Context, userID string) error {
	u, err := o.store.GetUserByID(ctx, userID)
	if err == nil && u != nil {
		o.lastActiveAtAtEnsure = u.LastActiveAt
	}
	return o.delegate.EnsureRunning(ctx, userID)
}

// TestLoginBumpsActiveBeforeEnsure — structural regression guard for the
// Login/GC race fix. The persisted last_active_at must be ≥ the EnsureRunning
// entry timestamp; if Login bumped after Ensure (the bug), the persisted
// value would still be the prior signup-time stamp.
func TestLoginBumpsActiveBeforeEnsure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	stub := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))
	obs := &observingProvisioner{delegate: stub, store: st}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	svc := auth.New(st, obs, false, "", nil, log)

	mux := http.NewServeMux()
	svc.Register(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	postJSON := func(path string, body any) *http.Response {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		resp, err := client.Post(srv.URL+path, "application/json", &buf)
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		return resp
	}

	// Signup.
	postJSON("/signup", map[string]string{"email": "race@x.io", "password": "hunter22", "username": "racer"}).Body.Close()

	// Capture signup-time last_active_at so we can prove it changed by Ensure entry.
	pre, err := st.GetUserByEmail(context.Background(), "race@x.io")
	if err != nil {
		t.Fatalf("get pre: %v", err)
	}
	signupActive := pre.LastActiveAt

	// Sleep ≥1s so unix-seconds resolution can register a strictly-greater
	// timestamp — otherwise the bump-before-Ensure check is unprovable.
	time.Sleep(1100 * time.Millisecond)

	// Login.
	client.Jar, _ = cookiejar.New(nil)
	postJSON("/login", map[string]string{"email": "race@x.io", "password": "hunter22"}).Body.Close()

	// Assertion: the value observed at EnsureRunning entry is strictly
	// greater than the signup timestamp. That's only possible if Login
	// bumped before calling Ensure.
	if obs.lastActiveAtAtEnsure <= signupActive {
		t.Errorf("LastActiveAt at EnsureRunning entry (%d) did not advance past signup (%d) — login bumped after Ensure (race)",
			obs.lastActiveAtAtEnsure, signupActive)
	}
}
