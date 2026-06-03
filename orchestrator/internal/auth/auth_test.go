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
	"github.com/skipper/homa/orchestrator/internal/ratelimit"
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

// fillerEssay is a 20+ character placeholder used to auto-populate
// the application essay fields in tests that don't care about their
// content. Tests that validate the fields directly pass them
// explicitly (a present key in the body map suppresses the auto-fill).
const fillerEssay = "placeholder essay for tests — twenty chars"

// approve flips the approved gate for the given email so subsequent
// /login calls succeed. Without this, signups stay in PENDING state
// and login returns 403. Most happy-path tests need this; tests that
// exercise the pending-gate behavior call it conditionally.
func (e *testEnv) approve(email string) {
	e.t.Helper()
	u, err := e.store.GetUserByEmail(context.Background(), email)
	if err != nil {
		e.t.Fatalf("approve: lookup %s: %v", email, err)
	}
	if err := e.store.SetApproved(context.Background(), u.ID, true); err != nil {
		e.t.Fatalf("approve: SetApproved %s: %v", email, err)
	}
}

func (e *testEnv) post(path string, body any) *http.Response {
	e.t.Helper()
	// Auto-inject application essay fields on /signup so existing
	// happy-path tests didn't need to change for the field expansion.
	// Negative-validation tests can override by setting an empty
	// string (key present, value "" — auto-fill skips).
	if path == "/signup" {
		if m, ok := body.(map[string]string); ok {
			for _, k := range []string{"join_reason", "mystery_interest", "background"} {
				if _, present := m[k]; !present {
					m[k] = fillerEssay
				}
			}
		}
	}
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
	var got struct {
		UserID  string `json:"user_id"`
		Pending bool   `json:"pending"`
	}
	decodeBody(t, resp, &got)
	if got.UserID == "" {
		t.Fatal("empty user_id")
	}
	if !got.Pending {
		t.Error("expected pending=true (application gate)")
	}

	// Signup must NOT set a cookie — account is pending approval.
	// Operator runs `homa approve` first; the user then logs in.
	if c := cookieFromResp(resp); c != nil {
		t.Errorf("signup should not set cookie (pending approval); got %s", c.String())
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
	// Approve so login passes the gate; without this we'd get 403.
	env.approve("li@b.co")
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
	env.approve("wp@b.co")
	env.client.Jar, _ = cookiejar.New(nil)

	resp := env.post("/login", map[string]string{"email": "wp@b.co", "password": "wrongone"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", resp.StatusCode)
	}
	if cookieFromResp(resp) != nil {
		t.Error("cookie set on failed login")
	}
}

// TestLoginPendingApproval — bcrypt-valid creds but Approved=false →
// 403 with the operator-actionable error message. No cookie issued.
// Regression: this gate was added later; verifies signup→login (without
// homa approve) is properly blocked.
func TestLoginPendingApproval(t *testing.T) {
	env := newTestEnv(t)
	env.post("/signup", map[string]string{
		"email": "pending@x.io", "password": "hunter22", "username": "pending",
	}).Body.Close()
	// Note: NO env.approve() — leave user PENDING.
	env.client.Jar, _ = cookiejar.New(nil)

	resp := env.post("/login", map[string]string{
		"email": "pending@x.io", "password": "hunter22",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", resp.StatusCode)
	}
	if cookieFromResp(resp) != nil {
		t.Error("cookie issued despite pending approval")
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("pending review")) {
		t.Errorf("body missing 'pending review': %s", body)
	}
}

// TestApprovalUnblocksLogin — approve flips the gate, allowing the
// previously-blocked applicant to log in. Idempotent (second login
// after approval also works).
func TestApprovalUnblocksLogin(t *testing.T) {
	env := newTestEnv(t)
	env.post("/signup", map[string]string{
		"email": "later@x.io", "password": "hunter22", "username": "later",
	}).Body.Close()
	env.client.Jar, _ = cookiejar.New(nil)

	// Before approve: 403.
	r1 := env.post("/login", map[string]string{"email": "later@x.io", "password": "hunter22"})
	r1.Body.Close()
	if r1.StatusCode != http.StatusForbidden {
		t.Fatalf("pre-approve login: got %d, want 403", r1.StatusCode)
	}

	env.approve("later@x.io")

	// After approve: 200 + cookie.
	r2 := env.post("/login", map[string]string{"email": "later@x.io", "password": "hunter22"})
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("post-approve login: got %d, want 200", r2.StatusCode)
	}
	if cookieFromResp(r2) == nil {
		t.Error("post-approve login: no cookie")
	}
}

func TestMeWithCookie(t *testing.T) {
	env := newTestEnv(t)
	signup := env.post("/signup", map[string]string{"email": "me@b.co", "password": "hunter22", "username": "meuser"})
	var signupBody struct{ UserID string `json:"user_id"` }
	decodeBody(t, signup, &signupBody)
	// Approve + login so we have a valid cookie for the /me call (signup
	// no longer sets one — pending-approval gate).
	env.approve("me@b.co")
	loginResp := env.post("/login", map[string]string{"email": "me@b.co", "password": "hunter22"})
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login after approve: %d", loginResp.StatusCode)
	}

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
	// Approve + login so we have a session to log out from.
	env.approve("lo@b.co")
	loginResp := env.post("/login", map[string]string{"email": "lo@b.co", "password": "hunter22"})
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login after approve: %d", loginResp.StatusCode)
	}

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
		t.Fatal("no session token after login")
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
				"email":            fmt.Sprintf("u%d@b.co", i),
				"password":         "hunter22",
				"username":         fmt.Sprintf("user_%d", i),
				"join_reason":      fillerEssay,
				"mystery_interest": fillerEssay,
				"background":       fillerEssay,
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
		if m, ok := body.(map[string]string); ok {
			for _, k := range []string{"join_reason", "mystery_interest", "background"} {
				if _, present := m[k]; !present {
					m[k] = fillerEssay
				}
			}
		}
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
		if path == "/signup" {
			if m, ok := body.(map[string]string); ok {
				for _, k := range []string{"join_reason", "mystery_interest", "background"} {
					if _, present := m[k]; !present {
						m[k] = fillerEssay
					}
				}
			}
		}
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
	// Approve so login passes the gate.
	uByEmail, _ := st.GetUserByEmail(context.Background(), "le@x.io")
	if err := st.SetApproved(context.Background(), uByEmail.ID, true); err != nil {
		t.Fatalf("approve: %v", err)
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
		if path == "/signup" {
			if m, ok := body.(map[string]string); ok {
				for _, k := range []string{"join_reason", "mystery_interest", "background"} {
					if _, present := m[k]; !present {
						m[k] = fillerEssay
					}
				}
			}
		}
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
	// Approve so login passes the gate.
	if err := st.SetApproved(context.Background(), pre.ID, true); err != nil {
		t.Fatalf("approve: %v", err)
	}

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

// TestSignupHoneypotSilentDrop — a filled honeypot returns the same
// {pending:true} shape as a real success so the bot logs it as accepted,
// but NO user row is created. This is the cheapest bot defense and the
// one with no false-positive risk for real users (the field is hidden
// from them).
func TestSignupHoneypotSilentDrop(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/signup", map[string]string{
		"email": "bot@x.io", "password": "hunter22", "username": "botbot",
		"website": "https://buy-cheap.example",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200 (look-like-success)", resp.StatusCode)
	}
	// No user row in the store.
	if _, err := env.store.GetUserByEmail(context.Background(), "bot@x.io"); err == nil {
		t.Error("user was created despite honeypot trip")
	}
	// No cookie issued.
	if c := cookieFromResp(resp); c != nil {
		t.Error("cookie issued on honeypot trip")
	}
}

// TestSignupRateLimit — after 5 signups (the configured capacity) from
// the same RemoteAddr, the 6th attempt returns 429. Real users won't
// hit this; bots will.
func TestSignupRateLimit(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	prov := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	limit := ratelimit.New(2, time.Hour) // burst of 2 so the test is fast
	svc := auth.New(st, prov, false, "", nil, log).WithSignupRateLimit(limit)
	mux := http.NewServeMux()
	svc.Register(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	post := func(email, username string) *http.Response {
		body, _ := json.Marshal(map[string]string{
			"email": email, "password": "hunter22", "username": username,
			"join_reason":      fillerEssay,
			"mystery_interest": fillerEssay,
			"background":       fillerEssay,
		})
		resp, err := http.Post(srv.URL+"/signup", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		return resp
	}

	for i := 0; i < 2; i++ {
		r := post(fmt.Sprintf("u%d@x.io", i), fmt.Sprintf("user_%d", i))
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d", i+1, r.StatusCode)
		}
	}
	r := post("u3@x.io", "user_3")
	defer r.Body.Close()
	if r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("3rd call: got %d, want 429", r.StatusCode)
	}
	if h := r.Header.Get("Retry-After"); h == "" {
		t.Error("missing Retry-After header on 429")
	}
}

// TestForgot_HappyPath — POST /forgot with a known email records a row
// with user_id resolved.
func TestForgot_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	// Seed a user so the email matches.
	env.post("/signup", map[string]string{
		"email": "real@x.io", "password": "hunter22", "username": "realuser",
	}).Body.Close()

	resp := env.post("/forgot", map[string]string{
		"email": "real@x.io",
		"note":  "browser wipe",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct{ OK bool }
	json.NewDecoder(resp.Body).Decode(&body)
	if !body.OK {
		t.Error("ok=false")
	}
	// Verify row landed with the matched user_id.
	reqs, err := env.store.ListPasswordResetRequests(context.Background(), 30)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("rows: %d", len(reqs))
	}
	if reqs[0].Email != "real@x.io" || reqs[0].UserID == "" {
		t.Errorf("row: %+v", reqs[0])
	}
}

// TestForgot_UnknownEmail — same success-ish response, row stored with
// empty user_id. No enumeration leak.
func TestForgot_UnknownEmail(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/forgot", map[string]string{"email": "phantom@x.io"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("unknown email status: %d, want 200 (no enumeration leak)", resp.StatusCode)
	}
	reqs, _ := env.store.ListPasswordResetRequests(context.Background(), 30)
	if len(reqs) != 1 || reqs[0].UserID != "" {
		t.Errorf("rows: %+v", reqs)
	}
}

// TestForgot_HoneypotSilentDrop — bot fills website, no row created.
func TestForgot_HoneypotSilentDrop(t *testing.T) {
	env := newTestEnv(t)
	resp := env.post("/forgot", map[string]string{
		"email":   "anyone@x.io",
		"website": "https://buy-cheap.example",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("honeypot status: %d", resp.StatusCode)
	}
	reqs, _ := env.store.ListPasswordResetRequests(context.Background(), 30)
	if len(reqs) != 0 {
		t.Errorf("rows: %+v (honeypot should have dropped)", reqs)
	}
}

// TestForgot_RateLimit — after the configured burst, 429.
func TestForgot_RateLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	limit := ratelimit.New(2, time.Hour)
	svc := auth.New(st, nil, false, "", nil, log).WithForgotRateLimit(limit)
	mux := http.NewServeMux()
	svc.Register(mux, nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	post := func() *http.Response {
		body, _ := json.Marshal(map[string]string{"email": "a@b.co"})
		resp, err := http.Post(srv.URL+"/forgot", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		return resp
	}
	for i := 0; i < 2; i++ {
		r := post()
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("call %d: %d", i+1, r.StatusCode)
		}
	}
	r := post()
	defer r.Body.Close()
	if r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("3rd: got %d, want 429", r.StatusCode)
	}
}

// TestForgot_NoteTooLong — the 500-char cap is enforced server-side
// (matches the editor's maxlength). Boundary check.
func TestForgot_NoteTooLong(t *testing.T) {
	env := newTestEnv(t)
	long := strings.Repeat("x", 501)
	resp := env.post("/forgot", map[string]string{
		"email": "a@b.co",
		"note":  long,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
	// And no row was inserted.
	rows, _ := env.store.ListPasswordResetRequests(context.Background(), 30)
	if len(rows) != 0 {
		t.Errorf("rows after rejected request: %d", len(rows))
	}
}
