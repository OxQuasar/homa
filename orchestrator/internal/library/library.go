// Package library serves the operator-managed reference content
// (cfg.LibraryDir, typically ~/homa/data/library/) to public visitors at
//
//   GET /api/library/                                  → JSON list of top-level entries
//   GET /api/library/<subpath>/                        → JSON list of subdir entries
//   GET /api/library/<subpath>/<file>                  → raw file contents
//
// All requests are cookie-auth-gated. The same directory is bind-mounted
// into user sandboxes at /library so the LLM has read access for context.
//
// URL prefix is /api/library/ (not /library/) to avoid colliding with
// SvelteKit routes on the public site — SvelteKit owns /library as a
// landing page; the API path keeps file URLs orchestrator-served.
package library

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skipper/homa/orchestrator/internal/auth"
)

// CORSWrapper mirrors auth.CORSWrapper / forum.CORSWrapper.
type CORSWrapper func(http.Handler) http.Handler

// Handler serves the library tree. Constructed once at startup;
// auth-gated by Register.
type Handler struct {
	root string // absolute path, e.g. /home/quasar/homa/data/library
	log  *slog.Logger
}

// New constructs a Handler. If root is empty or doesn't exist, the
// handler will 404 every request — keeps the feature opt-in without
// surfacing config errors as 5xx.
func New(root string, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &Handler{root: abs, log: log}
}

// Register mounts GET /api/library/* on mux behind auth + CORS.
// Pattern: "/api/library/" with trailing slash, so net/http strips it
// and the rest goes to h.serve. corsWrap may be nil.
func (h *Handler) Register(mux *http.ServeMux, authSvc *auth.Service, corsWrap CORSWrapper) {
	if corsWrap == nil {
		corsWrap = func(next http.Handler) http.Handler { return next }
	}
	h2 := corsWrap(authSvc.RequireAuth(http.HandlerFunc(h.serve)))
	// Trailing-slash pattern matches everything under /api/library/
	// (Go 1.22+ ServeMux's documented behavior). Don't also register
	// the {path...} wildcard — it would conflict.
	mux.Handle("GET /api/library/", h2)
	mux.Handle("OPTIONS /api/library/", corsWrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
}

// Entry is one row in a directory listing — same shape regardless of
// whether the entry is a file or subdir.
type Entry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"` // bytes; 0 for dirs
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if h.root == "" {
		writeJSONError(w, http.StatusNotFound, "library not configured")
		return
	}

	// Strip the /api/library/ prefix; treat empty as root.
	sub := strings.TrimPrefix(r.URL.Path, "/api/library/")
	sub = strings.TrimPrefix(sub, "/")

	// Resolve against root + reject path traversal. filepath.Join cleans
	// the path; we then verify the result stays under root.
	full := filepath.Join(h.root, sub)
	if !strings.HasPrefix(full, h.root) {
		writeJSONError(w, http.StatusBadRequest, "invalid path")
		return
	}

	info, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		h.log.Error("library stat", "path", full, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "stat failed")
		return
	}

	if info.IsDir() {
		h.serveDir(w, full)
		return
	}
	h.serveFile(w, r, full)
}

// serveDir returns a JSON list of entries (files + subdirs), sorted
// alphabetically with dirs before files for a tidy tree-display.
func (h *Handler) serveDir(w http.ResponseWriter, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		h.log.Error("library readdir", "dir", dir, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "readdir failed")
		return
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		// Skip hidden entries (.git, .DS_Store, etc.) — operator-managed
		// content shouldn't accidentally expose vcs internals.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var size int64
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				size = info.Size()
			}
		}
		out = append(out, Entry{Name: e.Name(), IsDir: e.IsDir(), Size: size})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir // dirs first
		}
		return out[i].Name < out[j].Name
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// serveFile returns the file contents with a best-effort Content-Type.
// http.ServeFile handles range requests + sniffing for unknown types;
// we override the type for known source-code extensions that browsers
// would otherwise refuse to display inline (e.g. .py → application/octet-stream).
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, path string) {
	// Override mime for common source-code types so they render as text
	// in the browser instead of triggering a download.
	ext := strings.ToLower(filepath.Ext(path))
	if t := textMimeFor(ext); t != "" {
		w.Header().Set("Content-Type", t)
	}
	http.ServeFile(w, r, path)
}

// textMimeFor returns a text/* mime for source-code extensions that
// the stdlib's MIME map either doesn't know about or maps to
// application/octet-stream. Returning "" means: let http.ServeFile
// sniff (handles .png, .pdf, .json, .html, .md natively).
func textMimeFor(ext string) string {
	switch ext {
	case ".py", ".pyi":
		return "text/x-python; charset=utf-8"
	case ".go":
		return "text/x-go; charset=utf-8"
	case ".rs":
		return "text/rust; charset=utf-8"
	case ".sh", ".bash":
		return "text/x-shellscript; charset=utf-8"
	case ".yml", ".yaml":
		return "text/yaml; charset=utf-8"
	case ".toml":
		return "text/toml; charset=utf-8"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	}
	// Defer to stdlib's mime.TypeByExtension for the rest; if it
	// returns empty, http.ServeFile sniffs.
	return mime.TypeByExtension(ext)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
