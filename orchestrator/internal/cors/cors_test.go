package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/cors"
)

const previewBase = "https://gandiva.tailnet.ts.net"

// TestAllow — origin matching across scheme, host, port variations.
// Same host on any port = allow; different host or different scheme = deny.
func TestAllow(t *testing.T) {
	p := cors.New(previewBase)
	cases := []struct {
		origin string
		want   bool
	}{
		{"https://gandiva.tailnet.ts.net", true},          // default port 443
		{"https://gandiva.tailnet.ts.net:443", true},
		{"https://gandiva.tailnet.ts.net:10001", true},    // any port
		{"https://gandiva.tailnet.ts.net:10002", true},
		{"http://gandiva.tailnet.ts.net", false},          // wrong scheme
		{"https://evil.example.com", false},               // wrong host
		{"https://gandiva.tailnet.ts.net.evil.com", false},// suffix spoof
		{"", false},                                       // empty
		{"not-a-url", false},                              // unparseable
	}
	for _, tc := range cases {
		t.Run(tc.origin, func(t *testing.T) {
			if got := p.Allow(tc.origin); got != tc.want {
				t.Errorf("Allow(%q): got %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

// TestEmptyPreviewBaseURL — policy with no host configured denies everyone.
// Used at boot when PreviewBaseURL is unset (no preview features).
func TestEmptyPreviewBaseURL(t *testing.T) {
	p := cors.New("")
	if p.Allow("https://gandiva.tailnet.ts.net") {
		t.Error("empty policy should deny all origins")
	}
}

// TestNoSchemePreviewURL — operator might configure PreviewBaseURL as just
// "gandiva.tailnet.ts.net" without scheme. Defensive fallback parses the
// host portion correctly.
func TestNoSchemePreviewURL(t *testing.T) {
	p := cors.New("gandiva.tailnet.ts.net")
	if !p.Allow("https://gandiva.tailnet.ts.net:10001") {
		t.Error("scheme-less PreviewBaseURL should still allow https origins on its host")
	}
}

// TestMiddlewarePassesThrough — non-OPTIONS request to a same-origin call
// (no Origin header) passes through to the next handler with no CORS
// headers added. Allows tools like curl to hit the same endpoint.
func TestMiddlewarePassesThrough(t *testing.T) {
	p := cors.New(previewBase)
	called := false
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("next handler not called for no-Origin request")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("ACAO should not be set when no Origin in request")
	}
}

// TestMiddlewareAddsHeadersOnAllowedOrigin — allowed Origin gets echoed
// back in ACAO + Allow-Credentials + Vary headers.
func TestMiddlewareAddsHeadersOnAllowedOrigin(t *testing.T) {
	p := cors.New(previewBase)
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://gandiva.tailnet.ts.net:10001" {
		t.Errorf("ACAO: got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("ACAC: got %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary: got %q", got)
	}
}

// TestMiddlewareDeniesDisallowedOrigin — origin from a different host
// gets no CORS headers; next handler still runs (browser will reject
// the response on its end since CORS headers are absent).
func TestMiddlewareDeniesDisallowedOrigin(t *testing.T) {
	p := cors.New(previewBase)
	called := false
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("next handler must still run for cross-origin GETs (browser handles rejection)")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("ACAO must not be set for disallowed origin")
	}
}

// TestMiddlewareHandlesPreflight — OPTIONS request from an allowed
// origin returns 204 with the full Allow-* set; downstream handler
// NOT called (preflight is the middleware's responsibility).
func TestMiddlewareHandlesPreflight(t *testing.T) {
	p := cors.New(previewBase)
	called := false
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status: got %d, want 204", rec.Code)
	}
	if called {
		t.Error("preflight should short-circuit; next handler ran")
	}
	for _, header := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Max-Age",
	} {
		if rec.Header().Get(header) == "" {
			t.Errorf("preflight response missing %s header", header)
		}
	}
}

// TestMiddlewareForbidsDisallowedPreflight — OPTIONS from a disallowed
// origin gets 403 short-circuit. Better than letting the next handler
// try to serve OPTIONS as if it were a real request.
func TestMiddlewareForbidsDisallowedPreflight(t *testing.T) {
	p := cors.New(previewBase)
	h := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called on forbidden preflight")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("forbidden preflight status: got %d, want 403", rec.Code)
	}
}
