package library_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/cors"
	"github.com/skipper/homa/orchestrator/internal/library"
	"github.com/skipper/homa/orchestrator/internal/store"
)

const previewBase = "https://gandiva.tailnet.ts.net"
const cookieToken = "tokliblibtokliblibtokliblibtokli"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type rig struct {
	srv  *httptest.Server
	root string // the library root dir (operator writes here)
}

func newRig(t *testing.T, withAuth bool) *rig {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	st, err := store.Open(filepath.Join(tmp, "homa.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if withAuth {
		u := store.User{
			ID: "abcd1234", Email: "u@x", PasswordHash: "$2a", Username: "u",
			BranchName: "u/x", WorktreePath: "/wt", ContainerName: "c", NousPort: 1,
			PreviewPort: 2, PreviewServePort: 3, NousSessionID: "s",
			CreatedAt: 1, LastActiveAt: 1, LastMessageAt: 1,
		}
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		st.CreateWebSession(context.Background(), cookieToken, u.ID, 9_999_999_999)
	}

	authSvc := auth.New(st, nil, false, "", quietLog())
	policy := cors.New(previewBase)
	mux := http.NewServeMux()
	library.New(root, quietLog()).Register(mux, authSvc, policy.Middleware)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &rig{srv: srv, root: root}
}

func (r *rig) write(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) get(t *testing.T, path string, attachCookie bool) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", r.srv.URL+path, nil)
	if attachCookie {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookieToken})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestRequiresAuth(t *testing.T) {
	r := newRig(t, false) // no user; cookie won't match
	resp, _ := r.get(t, "/api/library/", false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: got %d, want 401", resp.StatusCode)
	}
}

func TestListsRoot(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/atlas/01.py", "code")
	r.write(t, "iching/directory.md", "# index")
	r.write(t, "greek/intro.md", "# greek")

	resp, body := r.get(t, "/api/library/", true)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var entries []library.Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name)
	}
	// Dirs sorted first, alphabetic; both iching and greek should appear.
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(entries), names)
	}
	if !entries[0].IsDir || !entries[1].IsDir {
		t.Errorf("expected both dirs: %+v", entries)
	}
	if entries[0].Name != "greek" || entries[1].Name != "iching" {
		t.Errorf("order: got %v, want [greek iching]", names)
	}
}

func TestListsSubdir(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/atlas/01.py", "code1")
	r.write(t, "iching/atlas/02.py", "code2")
	r.write(t, "iching/atlas/readme.md", "atlas readme")
	r.write(t, "iching/atlas/deep/nested.txt", "nested")

	resp, body := r.get(t, "/api/library/iching/atlas/", true)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var entries []library.Entry
	json.Unmarshal(body, &entries)
	if len(entries) != 4 {
		t.Errorf("got %d entries: %+v", len(entries), entries)
	}
	// dirs first → deep/, then files alphabetical
	if !entries[0].IsDir || entries[0].Name != "deep" {
		t.Errorf("first should be deep dir: %+v", entries[0])
	}
	// Files have sizes set
	for _, e := range entries[1:] {
		if e.IsDir {
			t.Errorf("expected file: %+v", e)
		}
		if e.Size == 0 {
			t.Errorf("file %q: size=0 (should be > 0)", e.Name)
		}
	}
}

func TestServesFile(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/atlas/code.py", "import numpy\nprint('hi')\n")

	resp, body := r.get(t, "/api/library/iching/atlas/code.py", true)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if string(body) != "import numpy\nprint('hi')\n" {
		t.Errorf("body: %q", body)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/x-python") {
		t.Errorf("Content-Type: got %q, want text/x-python*", ct)
	}
}

func TestServesMarkdown(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/directory.md", "# Index\n\nThe atlas...\n")

	resp, body := r.get(t, "/api/library/iching/directory.md", true)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "# Index") {
		t.Errorf("body missing content: %q", body)
	}
}

func TestNotFound(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/exists.py", "x")

	resp, _ := r.get(t, "/api/library/iching/missing.py", true)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: %d, want 404", resp.StatusCode)
	}
}

// TestPathTraversal — security-critical. ../../ escapes must be rejected.
func TestPathTraversal(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/safe.py", "ok")
	// Write a sensitive file ABOVE the library root.
	parent := filepath.Dir(r.root)
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Try to escape via ../ — http.Client + Go's URL parser will
	// often clean these, so we use a raw request to be sure.
	req, _ := http.NewRequest("GET", r.srv.URL+"/api/library/../secret.txt", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookieToken})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "SECRET") {
		t.Errorf("path traversal succeeded: body contains SECRET (%s)", body)
	}
}

func TestHidesDotfiles(t *testing.T) {
	r := newRig(t, true)
	r.write(t, "iching/visible.py", "x")
	r.write(t, "iching/.hidden", "secret")
	r.write(t, "iching/.git/HEAD", "ref")

	resp, body := r.get(t, "/api/library/iching/", true)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), ".hidden") || strings.Contains(string(body), ".git") {
		t.Errorf("dotfiles leaked: %s", body)
	}
	if !strings.Contains(string(body), "visible.py") {
		t.Errorf("visible.py missing: %s", body)
	}
}

func TestEmptyRootIs404Free(t *testing.T) {
	r := newRig(t, true)
	resp, body := r.get(t, "/api/library/", true)
	if resp.StatusCode != 200 {
		t.Errorf("status: %d (empty root should be 200 with [])", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("empty body: got %q, want []", body)
	}
}

func TestCORSPreflight(t *testing.T) {
	r := newRig(t, true)
	req, _ := http.NewRequest("OPTIONS", r.srv.URL+"/api/library/iching/", nil)
	req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight: %d", resp.StatusCode)
	}
}
