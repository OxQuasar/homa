package llmproxy

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// writeCreds creates a credentials file at path with the given access
// + refresh tokens. Returns the absolute path for the proxy.
func writeTestCreds(t *testing.T, dir, access, refresh string) string {
	t.Helper()
	path := filepath.Join(dir, ".credentials.json")
	c := Creds{ClaudeAiOauth: ClaudeAiOauth{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    4070908800000, // 2099-01-01 in unix millis — far future
	}}
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	return path
}

// fakeUpstream records what came in + returns canned responses.
type fakeUpstream struct {
	mu          sync.Mutex
	requests    []*http.Request
	bodies      [][]byte
	respond200  int32 // when > 0, this many subsequent calls return 200; otherwise 401
	server      *httptest.Server
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, r)
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		if atomic.LoadInt32(&f.respond200) > 0 {
			atomic.AddInt32(&f.respond200, -1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeUpstream) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeUpstream) lastAuth() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return ""
	}
	return f.requests[len(f.requests)-1].Header.Get("Authorization")
}

// proxyWithUpstream constructs a Proxy whose upstream is overridden
// to a test-server URL.
func proxyWithUpstream(t *testing.T, credsPath, upstreamURL string) *Proxy {
	t.Helper()
	p := New(credsPath, nil)
	u, _ := url.Parse(upstreamURL)
	p.upstream = u
	return p
}

// TestProxy_InjectsAuth — a request without auth gets the Bearer
// token added before forwarding.
func TestProxy_InjectsAuth(t *testing.T) {
	creds := writeTestCreds(t, t.TempDir(), "sk-ant-real-token-xyz", "rt-xyz")
	upstream := newFakeUpstream(t)
	atomic.StoreInt32(&upstream.respond200, 1)

	p := proxyWithUpstream(t, creds, upstream.server.URL)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-foo"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	if got := upstream.lastAuth(); got != "Bearer sk-ant-real-token-xyz" {
		t.Errorf("auth header: got %q", got)
	}
}

// TestProxy_StripsClientAuth — even if the client sends an
// Authorization header, the proxy replaces it. Prevents the LLM in
// the container from forging a different identity (or seeing what
// real auth looks like).
func TestProxy_StripsClientAuth(t *testing.T) {
	creds := writeTestCreds(t, t.TempDir(), "real-token", "rt")
	upstream := newFakeUpstream(t)
	atomic.StoreInt32(&upstream.respond200, 1)

	p := proxyWithUpstream(t, creds, upstream.server.URL)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer FAKE-TOKEN-FROM-CONTAINER")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if got := upstream.lastAuth(); got != "Bearer real-token" {
		t.Errorf("auth header: got %q, want 'Bearer real-token'", got)
	}
}

// TestProxy_PassesThroughBody — request body reaches upstream verbatim.
func TestProxy_PassesThroughBody(t *testing.T) {
	creds := writeTestCreds(t, t.TempDir(), "tok", "rt")
	upstream := newFakeUpstream(t)
	atomic.StoreInt32(&upstream.respond200, 1)

	p := proxyWithUpstream(t, creds, upstream.server.URL)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}],"model":"claude-foo"}`
	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if got := string(upstream.bodies[0]); got != body {
		t.Errorf("body: got %q, want %q", got, body)
	}
}

// TestProxy_HealthzReadsCreds — /healthz returns a status JSON with
// the access-token prefix.
func TestProxy_Healthz(t *testing.T) {
	creds := writeTestCreds(t, t.TempDir(), "sk-ant-12345678901234567890", "rt")
	p := New(creds, nil)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	resp, _ := http.Get(srv.URL + "/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if pre, _ := out["access_token_prefix"].(string); !strings.HasPrefix(pre, "sk-ant-1") {
		t.Errorf("token prefix: %v", out)
	}
}

// TestProxy_MissingCreds — proxy returns 503 when creds file is gone.
func TestProxy_MissingCreds(t *testing.T) {
	p := New(filepath.Join(t.TempDir(), "noexist.json"), nil)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	resp, _ := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: %d, want 503", resp.StatusCode)
	}
}

// TestProxy_InjectsRequiredHeaders — anthropic-version + anthropic-beta
// + user-agent are added when the client didn't send them.
func TestProxy_InjectsRequiredHeaders(t *testing.T) {
	creds := writeTestCreds(t, t.TempDir(), "tok", "rt")
	upstream := newFakeUpstream(t)
	atomic.StoreInt32(&upstream.respond200, 1)

	p := proxyWithUpstream(t, creds, upstream.server.URL)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	resp, _ := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	resp.Body.Close()
	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	got := upstream.requests[0]
	if got.Header.Get("anthropic-version") == "" {
		t.Error("missing anthropic-version")
	}
	if got.Header.Get("anthropic-beta") == "" {
		t.Error("missing anthropic-beta")
	}
	if got.Header.Get("user-agent") == "" {
		t.Error("missing user-agent")
	}
}

// TestProxy_StreamingResponse — SSE-style chunked response is piped
// through to the client incrementally, not buffered. We control the
// fake upstream to write a chunk, sleep, write another. The client
// reads each chunk as it arrives.
//
// This is the regression test for B1: the original implementation
// called io.ReadAll on the upstream body before returning, breaking
// streaming responses (which Anthropic uses for chat completions).
func TestProxy_StreamingResponse(t *testing.T) {
	creds := writeTestCreds(t, t.TempDir(), "tok", "rt")

	// Upstream that writes 3 chunks with a small gap between each.
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		for i, chunk := range []string{"data: a\n\n", "data: b\n\n", "data: c\n\n"} {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
			if i < 2 {
				time.Sleep(20 * time.Millisecond)
			}
		}
	}))
	t.Cleanup(upstreamSrv.Close)

	p := proxyWithUpstream(t, creds, upstreamSrv.URL)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	// Track when each `data:` line arrives. If the proxy buffers, all
	// arrive together near t=40ms; if it streams, they arrive at
	// roughly t=0, t=20, t=40.
	type arrival struct {
		line string
		at   time.Duration
	}
	var arrivals []arrival
	start := time.Now()
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			arrivals = append(arrivals, arrival{line: line, at: time.Since(start)})
		}
		if err != nil {
			break
		}
	}

	if len(arrivals) != 3 {
		t.Fatalf("got %d data lines; want 3", len(arrivals))
	}
	// First data line should arrive promptly — proxy not buffering.
	if arrivals[0].at > 15*time.Millisecond {
		t.Errorf("first data line at %v; proxy may be buffering", arrivals[0].at)
	}
	// Last data line at ~40ms means we observed the gap, not bunched.
	if arrivals[2].at < 30*time.Millisecond {
		t.Errorf("last data line at %v; chunks bunched (proxy buffering?)",
			arrivals[2].at)
	}
}

// TestProxy_RefreshOn401 — proxy detects upstream 401, calls the
// refresh endpoint (mocked), retries with new token. Verifies the
// retry succeeds with the refreshed token.
func TestProxy_RefreshOn401(t *testing.T) {
	credsPath := writeTestCreds(t, t.TempDir(), "old-token", "rt-old")

	// Mock OAuth refresh server.
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "new-token-fresh",
			"refresh_token": "rt-new",
			"expires_in": 28800
		}`))
	}))
	t.Cleanup(oauthSrv.Close)

	// Upstream that 401s with old-token, 200s with new-token.
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer new-token-fresh" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"expired"}`))
	}))
	t.Cleanup(upstreamSrv.Close)

	p := proxyWithUpstream(t, credsPath, upstreamSrv.URL)
	p.oauthTokenURL = oauthSrv.URL // override for test
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status: %d, body: %s", resp.StatusCode, body)
	}
	// Creds file rewritten with new token.
	data, _ := os.ReadFile(credsPath)
	if !strings.Contains(string(data), "new-token-fresh") {
		t.Errorf("creds file not updated: %s", data)
	}
}
