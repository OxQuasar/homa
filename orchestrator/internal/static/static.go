// Package static serves the editor SPA + login/signup pages.
//
// The SPA lives in ./dist, an embedded subtree built by ~/homa/editor/build.sh
// (which configures Vite's outDir to point here). A stub index.html is
// committed so `go build` succeeds before the SPA has ever been built.
//
// Routes (registered by Register):
//
//   GET /signup    → dist/index.html (SPA picks the form from the path)
//   GET /login     → dist/index.html
//   GET /editor    → dist/index.html
//   GET /assets/*  → static asset served from dist/assets/
//
// The catch-all GET / is owned by the mainsite proxy (cmd/homa wires it),
// which forwards to the mainsite vite container and falls back to
// IndexHandler() on upstream failure.
package static

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
)

// distFS embeds the editor's built output. The "all:" prefix preserves files
// whose names start with "." (Vite occasionally emits `.vite/` manifests).
//
//go:embed all:dist
var distFS embed.FS

// indexPath is the file inside the embed served for every SPA entry route.
const indexPath = "dist/index.html"

// Register mounts the static SPA-page + asset handlers on mux. Does NOT
// register the catch-all GET / — that's owned by proxy.RegisterMainSite
// (the mainsite reverse proxy) so the home page is served by the
// mainsite vite container, with the SPA index.html as fallback on
// upstream failure (see IndexHandler below).
func Register(mux *http.ServeMux, log *slog.Logger) (http.Handler, error) {
	if log == nil {
		log = slog.Default()
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	assetsFS, err := fs.Sub(sub, "assets")
	if err != nil {
		return nil, err
	}

	indexHandler := newIndexHandler(sub)

	// Assets are content-hashed by Vite — `index-<hash>.js`. Tell browsers
	// they're immutable; a new build produces a new filename and a new
	// fetch via the un-cached index.html.
	assetCacheWrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			next.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc("GET /signup", indexHandler)
	mux.HandleFunc("GET /login", indexHandler)
	mux.HandleFunc("GET /editor", indexHandler)
	mux.HandleFunc("GET /admin", indexHandler)
	mux.HandleFunc("GET /guidelines", indexHandler)
	mux.HandleFunc("GET /forgot", indexHandler)

	mux.Handle("GET /assets/", assetCacheWrap(http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS)))))

	return http.HandlerFunc(indexHandler), nil
}

// newIndexHandler returns a handler that serves dist/index.html with
// no-cache headers. Exposed via Register's return value so the mainsite
// proxy can use it as the upstream-failure fallback.
func newIndexHandler(sub fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "spa missing", http.StatusInternalServerError)
			return
		}
		// Critical: never cache index.html. It carries the hashed asset
		// URLs (e.g. /assets/index-<hash>.js); if a browser caches index
		// across a new build, it'll keep loading the OLD bundle and miss
		// any UI-level fixes. The hashed assets themselves are immutable
		// by name so they can be cached aggressively above.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
}
