package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// uploadHarness wires a Service onto an httptest server with a real auth
// service backed by a fresh SQLite store. Each test gets a clean rig.
// The cookie token is shared across tests (per-harness state isolated by
// t.TempDir / a fresh DB).
const harnessCookieToken = "deadbeefdeadbeefdeadbeefdeadbeef"

type uploadHarness struct {
	server      *httptest.Server
	branchesDir string
	userID      string
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newHarness(t *testing.T) *uploadHarness {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "homa.db")
	branches := filepath.Join(tmp, "branches")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Seed a user. The auth flow normally provisions worktree etc; for
	// the upload test we only need the row + cookie + the worktree dir.
	const userID = "abcd1234"
	if err := os.MkdirAll(filepath.Join(branches, userID), 0o755); err != nil {
		t.Fatalf("mkdir branches: %v", err)
	}
	u := store.User{
		ID: userID, Email: "u@x", PasswordHash: "$2a", Name: "U",
		BranchName: "user/" + userID, WorktreePath: filepath.Join(branches, userID),
		ContainerName: "homa-user-" + userID,
		NousPort:      40000, PreviewPort: 40001, PreviewServePort: 10001,
		NousSessionID: "sess",
		CreatedAt:     1, LastActiveAt: 1, LastMessageAt: 1,
	}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Issue a cookie directly via the store.
	if err := st.CreateWebSession(context.Background(), harnessCookieToken, userID, 9_999_999_999); err != nil {
		t.Fatalf("CreateWebSession: %v", err)
	}

	log := quietLog()
	// Tiny size limit in tests so the 413 path is cheap to exercise.
	authSvc := auth.New(st, nil, false, "", nil, log)
	svc := New(branches, 1024 /* 1 KiB */, log)
	mux := http.NewServeMux()
	svc.Register(mux, authSvc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &uploadHarness{
		server:      srv,
		branchesDir: branches,
		userID:      userID,
	}
}

// postUpload sends a multipart POST /upload with `body` as the file
// content under filename `filename`. Always attaches the auth cookie.
func (h *uploadHarness) postUpload(t *testing.T, filename string, body []byte) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
	mw.Close()

	req, _ := http.NewRequest("POST", h.server.URL+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: harnessCookieToken})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// --- tests --------------------------------------------------------------

// TestUploadHappyPath — a small file lands in branches/<uid>/static/uploads/
// with the response carrying both the worktree-relative `path` and the
// browser-facing `public_path` (sveltekit static convention).
func TestUploadHappyPath(t *testing.T) {
	h := newHarness(t)
	resp, body := h.postUpload(t, "hero.jpg", []byte("FAKEJPEGBYTES"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", resp.StatusCode, body)
	}
	var got uploadResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, body)
	}
	if got.Path != "static/uploads/hero.jpg" {
		t.Errorf("Path: got %q, want static/uploads/hero.jpg", got.Path)
	}
	if got.PublicPath != "/uploads/hero.jpg" {
		t.Errorf("PublicPath: got %q, want /uploads/hero.jpg", got.PublicPath)
	}
	if got.Size != 13 {
		t.Errorf("Size: got %d, want 13", got.Size)
	}
	// File actually on disk?
	target := filepath.Join(h.branchesDir, h.userID, "static", "uploads", "hero.jpg")
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(contents) != "FAKEJPEGBYTES" {
		t.Errorf("disk contents: got %q", contents)
	}
}

// TestUploadCollisionRenames — second upload with the same filename
// doesn't clobber; lands as `hero-1.jpg`, response reports the new path.
func TestUploadCollisionRenames(t *testing.T) {
	h := newHarness(t)
	h.postUpload(t, "hero.jpg", []byte("first"))
	resp, body := h.postUpload(t, "hero.jpg", []byte("second"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var got uploadResp
	json.Unmarshal(body, &got)
	if got.Path != "static/uploads/hero-1.jpg" {
		t.Errorf("collision path: got %q, want static/uploads/hero-1.jpg", got.Path)
	}
	// First file untouched.
	first, _ := os.ReadFile(filepath.Join(h.branchesDir, h.userID, "static", "uploads", "hero.jpg"))
	if string(first) != "first" {
		t.Errorf("original clobbered: %q", first)
	}
	second, _ := os.ReadFile(filepath.Join(h.branchesDir, h.userID, "static", "uploads", "hero-1.jpg"))
	if string(second) != "second" {
		t.Errorf("renamed second: %q", second)
	}
}

// TestUploadConcurrentCollisionsAllSucceed — N parallel uploads of the
// same name must all land (foo, foo-1, foo-2, …) without clobbering and
// without two grabbing the same slot. Exercises the O_EXCL atomicity.
func TestUploadConcurrentCollisionsAllSucceed(t *testing.T) {
	h := newHarness(t)
	const N = 5
	var wg sync.WaitGroup
	results := make([]string, N)
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := h.postUpload(t, "race.txt", []byte("body"+string(rune('A'+i))))
			if resp.StatusCode != http.StatusOK {
				t.Errorf("worker %d: status %d body=%s", i, resp.StatusCode, body)
				return
			}
			var got uploadResp
			json.Unmarshal(body, &got)
			results[i] = got.Path
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, p := range results {
		if p == "" {
			t.Errorf("worker %d: empty path", i)
			continue
		}
		if seen[p] {
			t.Errorf("path %q allocated twice", p)
		}
		seen[p] = true
	}
	if len(seen) != N {
		t.Errorf("distinct paths: got %d, want %d", len(seen), N)
	}
}

// TestUploadFilenameSanitization — paths, hidden-file prefixes, and
// weird charsets are normalized. The original-name semantics survive
// for the common case (alphanumeric + .-_); everything else becomes '-'.
func TestUploadFilenameSanitization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"normal.jpg", "normal.jpg"},
		{"../../etc/passwd", "passwd"},                // basename + safe chars
		{"...hidden.jpg", "hidden.jpg"},               // strip leading dots
		{"my cool photo!.png", "my-cool-photo-.png"},  // spaces + ! → -
		{"日本語.jpg", "---.jpg"},                     // 3 non-ASCII runes → 3 dashes
		{"/absolute/path.txt", "path.txt"},            // strip dir
		{"trailing/", "trailing"},                     // filepath.Base strips trailing slash
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := sanitizeFilename(tc.in); got != tc.want {
				t.Errorf("sanitizeFilename(%q): got %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestUploadOverLimitReturns413 — file larger than maxBytes (1024 in
// the test harness) returns 413 with an explanatory body, and DOES NOT
// leave a partial file on disk.
func TestUploadOverLimitReturns413(t *testing.T) {
	h := newHarness(t)
	huge := bytes.Repeat([]byte("X"), 2048)
	resp, body := h.postUpload(t, "huge.bin", huge)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d, want 413; body=%s", resp.StatusCode, body)
	}
	// No partial file under any expected name.
	dir := filepath.Join(h.branchesDir, h.userID, "static", "uploads")
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("partial file left behind: %v", names)
	}
}

// TestUploadMissingCookieReturns401 — no auth cookie → 401, no file
// written. RequireAuth middleware does the gating; this test pins that
// the upload endpoint is actually mounted behind it.
func TestUploadMissingCookieReturns401(t *testing.T) {
	h := newHarness(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.txt")
	fw.Write([]byte("body"))
	mw.Close()
	req, _ := http.NewRequest("POST", h.server.URL+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// NO cookie added.
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

// TestUploadMissingFileFieldReturns400 — multipart present but no
// `file` field → 400 with a clear error message.
func TestUploadMissingFileFieldReturns400(t *testing.T) {
	h := newHarness(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormField("text") // wrong field name
	fw.Write([]byte("hello"))
	mw.Close()
	req, _ := http.NewRequest("POST", h.server.URL+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: harnessCookieToken})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

// TestSanitizeRejectsAllUnsafe — pure-unsafe names return "" so the
// handler can 400 cleanly rather than writing an empty file.
func TestSanitizeRejectsAllUnsafe(t *testing.T) {
	for _, in := range []string{"", ".", "..", "/", "...", "./.", "/.."} {
		if got := sanitizeFilename(in); got != "" {
			t.Errorf("sanitizeFilename(%q): got %q, want empty", in, got)
		}
	}
	// Long name truncation: confirm we don't lose the extension.
	long := strings.Repeat("a", 300) + ".jpg"
	got := sanitizeFilename(long)
	if !strings.HasSuffix(got, ".jpg") {
		t.Errorf("long name lost extension: got %q", got)
	}
	if len(got) > 240 {
		t.Errorf("long name not truncated: len %d", len(got))
	}
}
