package proxy_test

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/proxy"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestProxyForwardsToUpstream — when the upstream answers, the response
// passes through verbatim (status + body).
func TestProxyForwardsToUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-From", "upstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-body path=" + r.URL.Path))
	}))
	t.Cleanup(upstream.Close)
	port := upstreamPort(t, upstream)

	mux := http.NewServeMux()
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fallback", http.StatusOK)
	})
	proxy.RegisterMainSite(mux, port, fallback, quietLog())

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/some/path")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-From"); got != "upstream" {
		t.Errorf("X-From: got %q, want upstream", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "upstream-body path=/some/path") {
		t.Errorf("body: got %q", body)
	}
}

// TestProxyFallsBackWhenUpstreamDown — connection refused (no upstream
// listening) routes to the fallback handler. Visitors should see the
// SPA login page rather than a 502 while mainsite is warming up.
func TestProxyFallsBackWhenUpstreamDown(t *testing.T) {
	// Pick a port nothing's listening on. A momentary bind+close gives us
	// an OS-allocated port that's almost certainly free a moment later.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // free it — proxy will fail to dial

	mux := http.NewServeMux()
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-From", "fallback")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fallback-body"))
	})
	proxy.RegisterMainSite(mux, port, fallback, quietLog())

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200 (fallback)", resp.StatusCode)
	}
	if got := resp.Header.Get("X-From"); got != "fallback" {
		t.Errorf("X-From: got %q, want fallback", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fallback-body" {
		t.Errorf("body: got %q, want fallback-body", body)
	}
}

// TestProxyDoesNotShadowSpecificRoutes — a more specific GET pattern
// registered alongside the catch-all wins. This is the routing precedence
// cmd/homa relies on to keep /editor, /assets/, /signup, /login pointing
// at the SPA instead of being proxied.
func TestProxyDoesNotShadowSpecificRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("MAIN"))
	}))
	t.Cleanup(upstream.Close)
	port := upstreamPort(t, upstream)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /editor", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("SPA-EDITOR"))
	})
	proxy.RegisterMainSite(mux, port, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("FB"))
	}), quietLog())

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	for _, tc := range []struct {
		path, want string
	}{
		{"/editor", "SPA-EDITOR"}, // specific wins
		{"/", "MAIN"},             // catch-all
		{"/about", "MAIN"},        // catch-all
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(front.URL + tc.path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if string(b) != tc.want {
				t.Errorf("%s body: got %q, want %q", tc.path, b, tc.want)
			}
		})
	}
}

// upstreamPort extracts the numeric port of an httptest.Server's URL.
func upstreamPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u := srv.URL
	// strip "http://"
	if !strings.HasPrefix(u, "http://") {
		t.Fatalf("unexpected url: %s", u)
	}
	hostPort := strings.TrimPrefix(u, "http://")
	_, p, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split host:port: %v", err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port not int: %v", err)
	}
	return n
}
