// Package static serves the editor SPA + login/signup pages.
//
// The SPA lives in ./dist, an embedded subtree built by ~/homa/editor/build.sh
// (which configures Vite's outDir to point here). A stub index.html is
// committed so `go build` succeeds before the SPA has ever been built.
//
// Routes:
//
//   GET /          → 302 /editor (if logged in) or 302 /login
//   GET /signup    → dist/index.html (SPA picks the form from the path)
//   GET /login     → dist/index.html
//   GET /editor    → dist/index.html
//   GET /assets/*  → static asset served from dist/assets/
//   anything else  → 404
package static

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/skipper/homa/orchestrator/internal/auth"
)

// distFS embeds the editor's built output. The "all:" prefix preserves files
// whose names start with "." (Vite occasionally emits `.vite/` manifests).
//
//go:embed all:dist
var distFS embed.FS

// indexPath is the file inside the embed served for every SPA entry route.
const indexPath = "dist/index.html"

// Register mounts the static + redirect handlers on mux.
//
// The auth service is needed only for read-only cookie lookups on /, so we
// take *auth.Service rather than duplicating the cookie+store dance. (Per
// the captain's spec — the LookupCookie helper was extracted in auth.)
func Register(mux *http.ServeMux, authSvc *auth.Service, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return err
	}
	assetsFS, err := fs.Sub(sub, "assets")
	if err != nil {
		return err
	}

	indexHandler := func(w http.ResponseWriter, _ *http.Request) {
		b, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "spa missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Only "/" itself is a redirect; anything else under "/" is 404
		// (assets and entry routes have their own explicit handlers).
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		dest := "/login"
		if authSvc.LookupCookie(r) != nil {
			dest = "/editor"
		}
		http.Redirect(w, r, dest, http.StatusFound)
	})

	mux.HandleFunc("GET /signup", indexHandler)
	mux.HandleFunc("GET /login", indexHandler)
	mux.HandleFunc("GET /editor", indexHandler)

	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))

	return nil
}
