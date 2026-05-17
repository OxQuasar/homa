// Package cors implements the CORS middleware homa uses for cross-origin
// API access. The motivating case: a user's site renders inside an
// iframe at `<host>:<user-port>/` (per-user tailscale-serve port); it
// needs to call orchestrator APIs at `<host>:443/`. Same host, different
// ports → different origins per CORS rules, so the response needs the
// usual Allow-* headers + the browser needs a preflight handled.
//
// Origin policy: allow any port on the configured host. The configured
// host comes from PreviewBaseURL (same source the editor uses to build
// per-user URLs). A request from `https://gandiva.tailnet:443/` (main
// site) gets through too; from `https://other-host/` does not.
//
// Credentials: enabled. Cookies aren't port-scoped, so the homa session
// cookie set on port 443 travels with cross-port requests; we need
// `Access-Control-Allow-Credentials: true` for the browser to surface
// that response to the calling JS.
package cors

import (
	"net/http"
	"net/url"
	"strings"
)

// Policy decides which origins to allow. Constructed from the operator's
// PreviewBaseURL; the host portion is the matching key.
type Policy struct {
	// host is the hostname (no scheme, no port). Origins whose Hostname()
	// matches this — with the same scheme — are allowed on any port.
	host string
	// scheme is "https" in production, "http" in tests / local dev.
	scheme string
}

// New parses previewBaseURL and returns a Policy. Empty URL → Policy
// whose Allow returns false for every origin (CORS effectively off);
// downstream handlers still work for same-origin requests since the
// middleware just doesn't add headers in that case.
func New(previewBaseURL string) *Policy {
	if previewBaseURL == "" {
		return &Policy{}
	}
	u, err := url.Parse(previewBaseURL)
	if err != nil {
		return &Policy{}
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	// url.Parse on "gandiva.tailnet" (no scheme) puts the whole string in
	// Path, not Host. Defensive fallback: pull from raw if Host is empty.
	host := u.Hostname()
	if host == "" {
		raw := strings.TrimPrefix(previewBaseURL, scheme+"://")
		if i := strings.IndexAny(raw, "/:"); i >= 0 {
			raw = raw[:i]
		}
		host = raw
	}
	return &Policy{host: host, scheme: scheme}
}

// Allow reports whether the given Origin header value matches the policy.
// Empty origin → false (no CORS headers added; same-origin handles it).
func (p *Policy) Allow(origin string) bool {
	if p.host == "" || origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Scheme == p.scheme && u.Hostname() == p.host
}

// Middleware wraps next with CORS headers. For matching origins:
//   - Access-Control-Allow-Origin: <echoed origin>
//   - Access-Control-Allow-Credentials: true
//   - Vary: Origin  (so caches don't cross-pollinate responses)
//
// For OPTIONS preflight: also writes Allow-Methods + Allow-Headers and
// short-circuits with 204. For non-matching origins: passes through with
// no CORS headers (browser then rejects on its end, which is the
// intended behaviour).
func (p *Policy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if p.Allow(origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				// Content-Type covers JSON POSTs. We don't accept custom
				// auth headers — cookies are the auth mechanism.
				h.Set("Access-Control-Allow-Headers", "Content-Type")
				h.Set("Access-Control-Max-Age", "600") // 10 min cache
				w.WriteHeader(http.StatusNoContent)
				return
			}
		} else if r.Method == http.MethodOptions {
			// Disallowed OPTIONS preflight — short-circuit with 403 so
			// the browser reports a clean error instead of letting the
			// next handler try to process it.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
