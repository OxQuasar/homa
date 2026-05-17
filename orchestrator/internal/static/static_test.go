package static_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/static"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// newTestMux mounts auth + static on a fresh mux backed by a temp DB and
// stub provisioner. The returned httptest.Server is closed via t.Cleanup.
//
// NOTE on /: static.Register no longer claims GET /. cmd/homa wires that
// to either the mainsite reverse proxy or the SPA index fallback. These
// tests cover the static-package-only mux — root requests 404 here.
func newTestMux(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	prov := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	authSvc := auth.New(st, prov, false, "", log)

	mux := http.NewServeMux()
	authSvc.Register(mux, nil)
	if _, err := static.Register(mux, log); err != nil {
		t.Fatalf("static.Register: %v", err)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		// Don't follow redirects so tests can assert on the 302.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return srv, client
}

// TestRootIs404WithoutMainsite — when no GET / handler is registered
// (this package's tests don't wire mainsite), the bare host returns 404.
// In production cmd/homa always registers something at GET / (either the
// mainsite proxy or the SPA fallback).
func TestRootIs404WithoutMainsite(t *testing.T) {
	srv, client := newTestMux(t)
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (static package alone does not own /)", resp.StatusCode)
	}
}

// TestRegisterReturnsFallbackHandler — Register returns the SPA index
// handler so cmd/homa can pass it as the mainsite proxy's fallback. Sanity-
// check it actually serves the SPA.
func TestRegisterReturnsFallbackHandler(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	mux := http.NewServeMux()
	fallback, err := static.Register(mux, log)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/anything", nil)
	fallback.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback status: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("fallback Content-Type: got %q, want text/html…", ct)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("<html")) {
		t.Errorf("fallback body missing <html>")
	}
}

func TestEntryRoutesServeIndex(t *testing.T) {
	srv, client := newTestMux(t)
	for _, path := range []string{"/signup", "/login", "/editor"} {
		t.Run(path, func(t *testing.T) {
			resp, err := client.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("get %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type: got %q, want text/html...", ct)
			}
			b, _ := io.ReadAll(resp.Body)
			if !bytes.Contains(b, []byte("<html")) {
				t.Errorf("body missing <html>: %s", b)
			}
		})
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	srv, client := newTestMux(t)
	resp, err := client.Get(srv.URL + "/totally-not-a-route")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

// TestAssetsServed verifies the /assets/ subtree maps to dist/assets.
// We write a marker file into the embed dir up-front via the build pipeline;
// here we just confirm that 404 is returned for a non-existent asset
// (positive case requires the real Vite build).
func TestAssetsMissingFile404(t *testing.T) {
	srv, client := newTestMux(t)
	resp, err := client.Get(srv.URL + "/assets/does-not-exist.css")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

// TestExistingPostRoutesUnchanged verifies that mounting static.Register on
// the same mux as auth.Register doesn't clobber the POST handlers.
func TestExistingPostRoutesUnchanged(t *testing.T) {
	srv, client := newTestMux(t)
	body, _ := json.Marshal(map[string]string{"email": "p@b.co", "password": "hunter22", "username": "pbcouser"})
	resp, err := client.Post(srv.URL+"/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /signup status: got %d, want 200", resp.StatusCode)
	}
	// And the cookie was issued.
	u, _ := url.Parse(srv.URL)
	cookies := client.Jar.Cookies(u)
	found := false
	for _, c := range cookies {
		if c.Name == auth.CookieName {
			found = true
		}
	}
	if !found {
		t.Error("homa_session cookie missing after signup")
	}
}
