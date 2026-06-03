// Package llmproxy is an authenticating reverse-proxy in front of
// api.anthropic.com. User containers send their LLM requests here
// (set via nous's base_url config) WITHOUT credentials; the proxy
// adds the operator's OAuth bearer token and forwards.
//
// Why this exists: previously the operator's Claude Code credentials
// file (~/.claude/.credentials.json) was bind-mounted into every user
// sandbox so nous could authenticate. That gave a misaligned or
// jailbroken sandbox LLM a direct read on the operator's OAuth token
// (mode 600 root, but the LLM's tools run as root inside the container).
// The proxy is the close: credentials live ONLY on the host; containers
// have no path to them.
//
// On 401 from upstream, the proxy attempts an OAuth refresh (using the
// refresh_token stored alongside the access_token) and retries once.
// Refresh is single-flighted across concurrent requests.
package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// upstreamBase is the real Anthropic API root the proxy forwards to.
	upstreamBase = "https://api.anthropic.com"

	// oauthTokenURL is the claude.ai endpoint that exchanges a
	// refresh_token for a new access_token (+ rotated refresh_token).
	oauthTokenURL = "https://claude.ai/v1/oauth/token"

	// oauthClientID is the public OAuth client ID Claude Code uses.
	// Same value lives in nous's auth/oauth_refresh.go.
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// defaultExpiresIn — fallback when the OAuth response omits
	// expires_in. 8h matches Claude Code's typical access token lifetime.
	defaultExpiresIn = 28800

	// refreshTimeout caps a single OAuth round-trip.
	refreshTimeout = 60 * time.Second

	// refreshCooldown — minimum interval between refresh attempts.
	// Stops a thundering herd on a token boundary: many concurrent
	// requests trip 401 at the same time, single-flight collapses
	// them to one refresh call, the rest see the new token without
	// re-triggering.
	refreshCooldown = 30 * time.Second
)

// Proxy authenticates Anthropic API calls on behalf of nous instances
// running inside user containers.
type Proxy struct {
	credsPath     string
	upstream      *url.URL
	oauthTokenURL string // overrideable for tests; defaults to claude.ai
	client        *http.Client // dedicated to upstream calls
	log           *slog.Logger

	// Single-flight refresh: serialize the OAuth dance so concurrent
	// 401s on the same token boundary collapse to one refresh.
	refreshMu   sync.Mutex
	lastRefresh time.Time
}

// New constructs a Proxy. credsPath is the absolute path to the
// operator's Claude Code credentials file (typically
// ~/.claude/.credentials.json). log defaults to slog.Default.
func New(credsPath string, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	u, _ := url.Parse(upstreamBase)
	return &Proxy{
		credsPath:     credsPath,
		upstream:      u,
		oauthTokenURL: oauthTokenURL,
		client:        &http.Client{Timeout: 5 * time.Minute},
		log:           log,
	}
}

// ServeHTTP implements the proxy. Reads the operator's creds, adds
// auth headers, forwards to api.anthropic.com. On 401 → refresh once
// → retry once. All other status codes pass through verbatim.
//
// Streaming responses (e.g. SSE chat completions) are piped straight
// through — we don't buffer the response body. The 401-retry path
// only needs to inspect the status code, which is available before
// any body bytes flow. So:
//   200/streaming: io.Copy upstream→client, no buffering
//   401 once     : drain + close, refresh, retry once, then pipe
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		p.healthz(w)
		return
	}

	creds, err := p.loadCreds()
	if err != nil {
		p.log.Error("llmproxy: load creds", "err", err)
		http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
		return
	}

	// Read the request body once so we can replay on retry. Bounded
	// by Anthropic's request-size limit (a few MB) — acceptable to
	// buffer.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	resp, err := p.do(r.Context(), r, body, creds.ClaudeAiOauth.AccessToken)
	if err != nil {
		p.log.Error("llmproxy: upstream call failed", "err", err)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}

	// Refresh + retry on 401 — happens BEFORE any body is read, so
	// we don't buffer the streaming response of a successful retry.
	if resp.StatusCode == http.StatusUnauthorized {
		// Drain + close the 401 body before reusing the connection's
		// resources. Body is small (error JSON), cheap to discard.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		p.log.Info("llmproxy: 401 from upstream — refreshing")
		if err := p.refresh(); err != nil {
			p.log.Warn("llmproxy: refresh failed", "err", err)
			// Fall through to writing the 401 — but we already drained.
			// Build a synthetic 401 response so the client sees something.
			http.Error(w, "authentication failed and refresh unsuccessful",
				http.StatusUnauthorized)
			return
		}
		newCreds, err := p.loadCreds()
		if err != nil {
			http.Error(w, "post-refresh creds load failed", http.StatusInternalServerError)
			return
		}
		resp, err = p.do(r.Context(), r, body, newCreds.ClaudeAiOauth.AccessToken)
		if err != nil {
			p.log.Error("llmproxy: upstream call failed after refresh", "err", err)
			http.Error(w, "upstream unreachable after refresh", http.StatusBadGateway)
			return
		}
	}

	p.streamResponse(w, resp)
}

// do issues a single upstream request and returns the response WITH
// the body still open. Caller is responsible for closing resp.Body.
// Returns (nil, err) when the dial fails entirely.
func (p *Proxy) do(ctx context.Context, orig *http.Request, body []byte, accessToken string) (*http.Response, error) {
	upstream, err := http.NewRequestWithContext(ctx, orig.Method,
		p.upstream.String()+orig.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build upstream req: %w", err)
	}
	// Copy headers (except hop-by-hop), then override auth + OAuth-required.
	copyHeaders(upstream.Header, orig.Header)
	upstream.Header.Del("Authorization")
	upstream.Header.Del("x-api-key")
	upstream.Header.Set("Authorization", "Bearer "+accessToken)
	upstream.Header.Set("anthropic-version", "2023-06-01")
	if upstream.Header.Get("anthropic-beta") == "" {
		upstream.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
	}
	// User-Agent that the OAuth-issuing CLI uses — Anthropic gates
	// some endpoints on this signal.
	upstream.Header.Set("user-agent", "claude-cli/2.1.0 (external, cli)")

	return p.client.Do(upstream)
}

// streamResponse pipes upstream → client without buffering. Headers
// + status flush before body bytes flow, so streaming chunks arrive
// at the client incrementally — critical for SSE chat-completion
// responses (the editor's "watch the LLM type" experience).
func (p *Proxy) streamResponse(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	// Flush after each chunk so streaming bytes hit the wire promptly.
	// Without this, the standard library's buffered ResponseWriter
	// may collect chunks before emitting them.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 8*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client disconnected; stop reading upstream
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			p.log.Debug("llmproxy: upstream read", "err", err)
			return
		}
	}
}

// healthz returns a tiny status JSON. Operator can curl this to
// confirm the proxy reads the creds file + parses correctly.
// Deliberately does NOT issue any upstream call to api.anthropic.com
// so it stays cheap.
func (p *Proxy) healthz(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{}
	creds, err := p.loadCreds()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		out["err"] = err.Error()
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	tok := creds.ClaudeAiOauth.AccessToken
	if len(tok) > 12 {
		out["access_token_prefix"] = tok[:12] + "..."
	}
	if creds.ClaudeAiOauth.ExpiresAt > 0 {
		out["expires_at"] = time.UnixMilli(creds.ClaudeAiOauth.ExpiresAt).UTC().Format(time.RFC3339)
	}
	out["last_refresh"] = p.lastRefreshRO().UTC().Format(time.RFC3339)
	_ = json.NewEncoder(w).Encode(out)
}

func (p *Proxy) lastRefreshRO() time.Time {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	return p.lastRefresh
}

// copyHeaders copies hop-by-hop-safe headers from src to dst.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// isHopByHop is a small set of headers that should not cross proxy
// boundaries per RFC 7230 §6.1.
func isHopByHop(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailers",
		"transfer-encoding", "upgrade", "host":
		return true
	}
	return false
}

// --- creds + refresh -----------------------------------------------

// Creds matches the JSON layout of Claude Code's credentials file.
type Creds struct {
	ClaudeAiOauth ClaudeAiOauth `json:"claudeAiOauth"`
}

// ClaudeAiOauth holds the OAuth state. ExpiresAt is unix millis
// matching Claude Code's wire format. We don't actually consume it
// inside the proxy (forwarding doesn't check expiry — we react to
// 401s from upstream), but we preserve it on round-trip so claude-code
// CLI can still read the file.
type ClaudeAiOauth struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}

func (p *Proxy) loadCreds() (*Creds, error) {
	data, err := os.ReadFile(p.credsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p.credsPath, err)
	}
	var c Creds
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return &c, nil
}

// refresh exchanges the refresh_token for a new access_token and
// writes both back to the credentials file. Single-flighted and
// cooldown-gated so concurrent 401s collapse to one refresh.
func (p *Proxy) refresh() error {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if time.Since(p.lastRefresh) < refreshCooldown {
		// Someone refreshed within the cooldown window. Caller will
		// re-read the file + retry; if THAT 401s too, next attempt
		// will be > cooldown.
		p.log.Debug("llmproxy: skip refresh (recent)")
		return nil
	}

	creds, err := p.loadCreds()
	if err != nil {
		return fmt.Errorf("load creds: %w", err)
	}
	rt := creds.ClaudeAiOauth.RefreshToken
	if rt == "" {
		return fmt.Errorf("refresh token is empty")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {oauthClientID},
		"refresh_token": {rt},
	}
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.oauthTokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build refresh req: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Anchor expiry to request time, not response — slow round-trip
	// would otherwise overstate the remaining lifetime.
	issuedAt := time.Now().UTC()

	// Dedicated client: redirects disabled. Go's default client strips
	// POST body on 3xx, which would silently zero the access_token.
	rc := &http.Client{
		Timeout: refreshTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := rc.Do(req)
	if err != nil {
		return fmt.Errorf("oauth POST: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oauth refresh HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("parse refresh response: %w", err)
	}
	if r.Error != "" {
		return fmt.Errorf("oauth error: %s", r.Error)
	}
	if r.AccessToken == "" {
		return fmt.Errorf("oauth response missing access_token")
	}

	newRT := r.RefreshToken
	if newRT == "" {
		newRT = rt
	}
	exp := r.ExpiresIn
	if exp == 0 {
		exp = defaultExpiresIn
	}
	creds.ClaudeAiOauth.AccessToken = r.AccessToken
	creds.ClaudeAiOauth.RefreshToken = newRT
	creds.ClaudeAiOauth.ExpiresAt = issuedAt.Add(time.Duration(exp) * time.Second).UnixMilli()

	if err := p.writeCreds(creds); err != nil {
		return fmt.Errorf("write creds: %w", err)
	}
	p.lastRefresh = time.Now()
	p.log.Info("llmproxy: oauth token refreshed", "expires_in_s", exp)
	return nil
}

// writeCreds atomically rewrites the credentials file. Same pattern
// Claude Code uses — write tmp + rename — so a partial-write doesn't
// leave the operator without working credentials.
func (p *Proxy) writeCreds(c *Creds) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.credsPath + ".llmproxy.tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.credsPath)
}
