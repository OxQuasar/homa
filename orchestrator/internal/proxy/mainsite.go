// HTTP reverse proxy from the orchestrator's root to the mainsite vite
// container. Sits at the bottom of the routing precedence — anything not
// matched by an explicit auth/SPA/asset handler lands here.
//
// On upstream failure (vite not yet up, container crashed mid-tick),
// falls back to the static SPA login page so visitors see *something*
// rather than a 502.

package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// RegisterMainSite mounts the catch-all GET / handler that forwards to
// the mainsite vite at upstreamPort, with `fallback` serving requests
// that fail upstream. The fallback is typically the SPA login page so
// pre-merge / pre-warmup state still renders.
func RegisterMainSite(
	mux *http.ServeMux,
	upstreamPort int,
	fallback http.Handler,
	log *slog.Logger,
) {
	if log == nil {
		log = slog.Default()
	}
	upstream, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", upstreamPort))
	if err != nil {
		// Build-time misconfiguration; refuse to start rather than mask it.
		panic(fmt.Sprintf("mainsite proxy: bad upstream port %d: %v", upstreamPort, err))
	}
	rp := httputil.NewSingleHostReverseProxy(upstream)
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Warn("mainsite upstream unreachable; serving SPA fallback",
			"path", r.URL.Path, "err", err)
		fallback.ServeHTTP(w, r)
	}
	// GET / is the http.ServeMux catch-all: more specific patterns (e.g.
	// "GET /editor", "GET /assets/") win over it. POSTs and other methods
	// to / fall through to 405, which is correct — main is read-only.
	mux.HandleFunc("GET /", rp.ServeHTTP)
}
