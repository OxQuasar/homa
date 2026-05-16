// Package upload implements POST /upload — multipart file ingress from
// the editor into the authenticated user's worktree. Files land at
// branches/<userid>/static/uploads/<sanitized-filename> so the LLM can
// reference them by a stable relative path immediately afterward.
//
// Cookie-gated like /ws. Size-capped (default 10 MB). Atomic per-file
// via O_EXCL so concurrent uploads of the same name don't clobber —
// the second one transparently becomes foo-1.jpg, foo-2.jpg, …
package upload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/skipper/homa/orchestrator/internal/auth"
)

// Tunables.
const (
	// DefaultMaxBytes caps the in-flight multipart upload size. 10 MB is
	// generous for photos / screenshots; large enough that "I have a
	// 4MB jpg" works without needing op-level tuning, small enough that
	// a runaway upload can't fill the host disk in seconds.
	DefaultMaxBytes int64 = 10 << 20

	// uploadSubdir is where files land inside the user's worktree.
	// Under static/ because SvelteKit serves that subtree verbatim at
	// the root URL (static/uploads/foo.jpg → /uploads/foo.jpg in
	// browser). The `uploads/` namespace keeps these distinct from
	// LLM- or template-authored static assets.
	uploadSubdir = "static/uploads"

	// collisionRetries bounds the foo.jpg → foo-1.jpg → … walk. 100
	// is absurd headroom; in practice uploads collide once or twice.
	collisionRetries = 100
)

// Service handles POST /upload requests.
type Service struct {
	branchesDir string // absolute path; <branchesDir>/<userid>/ is the user's worktree
	maxBytes    int64
	log         *slog.Logger
}

// New constructs a Service. log may be nil → slog.Default(). maxBytes ≤ 0
// → DefaultMaxBytes.
func New(branchesDir string, maxBytes int64, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Service{
		branchesDir: branchesDir,
		maxBytes:    maxBytes,
		log:         log,
	}
}

// Register mounts POST /upload on mux behind auth.RequireAuth. The handler
// requires the form field `file` to carry the multipart payload.
func (s *Service) Register(mux *http.ServeMux, authSvc *auth.Service) {
	mux.Handle("POST /upload", authSvc.RequireAuth(http.HandlerFunc(s.handle)))
}

type uploadResp struct {
	// Path is the worktree-relative target — what the LLM should
	// reference. The editor pre-fills this into the chat so the next
	// prompt names the file directly.
	Path string `json:"path"`
	// PublicPath is the URL path the running site serves it from
	// (vite maps `static/X` to `/X`).
	PublicPath string `json:"public_path"`
	// Size is the bytes actually written.
	Size int64 `json:"size"`
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Cap the request body BEFORE parsing multipart so a hostile client
	// can't make us buffer GBs in `r.ParseMultipartForm`.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		// MaxBytesReader returns *MaxBytesError; surface 413 cleanly.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("file exceeds %d-byte limit", s.maxBytes))
			return
		}
		writeErr(w, http.StatusBadRequest, "parse multipart: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing form field 'file'")
		return
	}
	defer file.Close()

	safeName := sanitizeFilename(header.Filename)
	if safeName == "" {
		writeErr(w, http.StatusBadRequest, "filename empty after sanitization")
		return
	}

	dir := filepath.Join(s.branchesDir, u.ID, uploadSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log.Error("upload mkdir failed", "user_id", u.ID, "dir", dir, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Race-free target selection: O_EXCL ensures only one upload wins
	// the slot. Loop until a name is free or we exhaust retries.
	finalName, out, err := openUniqueFile(dir, safeName)
	if err != nil {
		s.log.Error("upload openUniqueFile failed", "user_id", u.ID, "name", safeName, "err", err)
		writeErr(w, http.StatusInternalServerError, "could not allocate filename")
		return
	}

	n, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		// Clean up the partial file so the user doesn't see an empty
		// foo-2.jpg next to nothing.
		_ = os.Remove(filepath.Join(dir, finalName))
		var mbe *http.MaxBytesError
		if errors.As(copyErr, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("file exceeds %d-byte limit", s.maxBytes))
			return
		}
		s.log.Error("upload copy failed", "user_id", u.ID, "err", copyErr)
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	case closeErr != nil:
		s.log.Error("upload close failed", "user_id", u.ID, "err", closeErr)
		writeErr(w, http.StatusInternalServerError, "close failed")
		return
	}

	relPath := filepath.Join(uploadSubdir, finalName)
	// SvelteKit static path: `static/uploads/foo.jpg` → `/uploads/foo.jpg`
	publicPath := "/" + strings.TrimPrefix(relPath, "static/")

	s.log.Info("upload accepted",
		"user_id", u.ID, "filename", finalName, "size", n, "path", relPath)

	writeJSON(w, http.StatusOK, uploadResp{
		Path:       relPath,
		PublicPath: publicPath,
		Size:       n,
	})
}

// sanitizeFilename strips path components + restricts to a conservative
// charset so the resulting name is safe to join under the user's
// worktree without traversal risk and renders in URLs without escaping.
//
// Returns "" if nothing safe remains (e.g. all dots / slashes).
func sanitizeFilename(raw string) string {
	// Take only the basename — defeats `../../etc/passwd` style paths
	// even before charset filtering.
	base := filepath.Base(raw)
	if base == "." || base == "/" {
		return ""
	}
	// Strip leading dots → no hidden files. Multiple leading dots
	// (...foo.jpg) become "foo.jpg".
	base = strings.TrimLeft(base, ".")
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	// "----.jpg" stays a valid name; only the truly-empty case is
	// rejected. Bound length so a 4kB filename can't blow up FS paths.
	const maxLen = 240
	if len(out) > maxLen {
		// Preserve extension when truncating.
		ext := filepath.Ext(out)
		stem := strings.TrimSuffix(out, ext)
		if keep := maxLen - len(ext); keep > 0 {
			out = stem[:keep] + ext
		} else {
			out = out[:maxLen]
		}
	}
	if out == "" {
		return ""
	}
	return out
}

// openUniqueFile tries name, then name-1.ext, name-2.ext, … until one
// succeeds with O_EXCL. Returns the chosen filename and an open file.
func openUniqueFile(dir, name string) (string, *os.File, error) {
	tryOpen := func(n string) (*os.File, error) {
		return os.OpenFile(filepath.Join(dir, n),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	}
	f, err := tryOpen(name)
	if err == nil {
		return name, f, nil
	}
	if !os.IsExist(err) {
		return "", nil, err
	}
	// Split into stem + ext so foo.jpg → foo-1.jpg, not foo.jpg-1.
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= collisionRetries; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		f, err := tryOpen(candidate)
		if err == nil {
			return candidate, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("exhausted %d collision retries for %q", collisionRetries, name)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
