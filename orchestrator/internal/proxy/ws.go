// Package proxy is the orchestrator's WebSocket reverse proxy from the
// browser to the user's sandbox nous daemon (mvp.md §7 + §14).
//
// The proxy is frame-opaque: it never decodes the nous wire protocol. Cookie
// authentication is enforced by auth.RequireAuth (mounted by Register); after
// that, the handler dials the user's sandbox on 127.0.0.1:<user.NousPort> and
// shuttles WebSocket messages bidirectionally until either side closes.
//
// Liveness: every connection bumps users.last_active_at on open and again
// at a fixed cadence while open, so the sandbox GC in internal/lifecycle
// can distinguish active users from disconnected ones via timestamp
// freshness alone — no separate WS registry needed.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// Tunables. Names match the captain's spec.
const (
	// upstreamDialTimeout caps how long we'll wait for the sandbox nous WS
	// to accept the connection. If the dial misses this window, we surface
	// a clean "sandbox unreachable" close to the browser.
	upstreamDialTimeout = 5 * time.Second

	// lastActiveTickInterval is how often we bump users.last_active_at while
	// a WS is open. 30s is short enough for an idle-GC pass to see liveness
	// without hammering the DB.
	lastActiveTickInterval = 30 * time.Second

	// shutdownGrace bounds how long Close calls take during teardown when
	// the parent context cancels (e.g. orchestrator SIGTERM).
	shutdownGrace = 5 * time.Second

	// ensureOnDialTimeout caps the wake-on-dial-refused recovery —
	// container start + readiness probe (vite warmup). Generous; the
	// alternative is the user gets bounced to login.
	ensureOnDialTimeout = 3 * time.Minute
)

// Register mounts the cookie-gated /ws reverse proxy onto mux. The auth
// service supplies both RequireAuth and the user-on-context contract.
// hub may be nil (legacy / test paths that don't need force-disconnect or
// server-push); when non-nil, Register associates every browser conn with
// its userID so lifecycle can act on them.
//
// prov may be nil — when non-nil, an upstream-dial-refused triggers a
// best-effort EnsureRunning followed by a single retry. Lets the editor
// auto-recover when a user's container has been idle-GC'd without
// requiring re-login.
func Register(mux *http.ServeMux, st *store.Store, authSvc *auth.Service, hub *Hub, prov provision.Provisioner, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	h := &handler{store: st, hub: hub, prov: prov, log: log}
	mux.Handle("GET /ws", authSvc.RequireAuth(http.HandlerFunc(h.serve)))
}

type handler struct {
	store *store.Store
	hub   *Hub                  // may be nil — register/unregister no-op if so
	prov  provision.Provisioner // may be nil — wake-on-dial-refused disabled
	log   *slog.Logger
}

// serve runs one browser↔sandbox WS proxy session. Lifecycle:
//
//  1. Resolve the authenticated user.
//  2. Accept the browser upgrade.
//  3. Dial the sandbox nous WS.
//  4. Bump last_active_at and start the periodic ticker.
//  5. Fan-out two goroutines (one per direction).
//  6. Wait for either side to error / close; tear both down.
func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		// RequireAuth should have prevented this; defensive guard.
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	// Accept the browser side first so we have somewhere to deliver any
	// upstream-dial error as a clean WS close (rather than a half-handled
	// HTTP response).
	browser, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Tailscale Serve / same-origin in MVP
	})
	if err != nil {
		h.log.Warn("ws accept failed", "user_id", u.ID, "err", err)
		return
	}
	browser.SetReadLimit(-1)
	defer browser.Close(websocket.StatusInternalError, "proxy ended")

	upstreamURL := fmt.Sprintf("ws://127.0.0.1:%d/", u.NousPort)
	upstream, err := h.dialUpstreamWithRecovery(r.Context(), u, upstreamURL)
	if err != nil {
		h.log.Warn("upstream dial failed", "user_id", u.ID, "url", upstreamURL, "err", err)
		_ = browser.Close(websocket.StatusInternalError, "sandbox unreachable")
		return
	}
	upstream.SetReadLimit(-1)
	defer upstream.Close(websocket.StatusGoingAway, "proxy ended")

	// Connection-scoped context: cancel when either side closes or when the
	// parent (HTTP request) is cancelled.
	connCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	h.bumpActive(connCtx, u.ID)
	go h.lastActiveTicker(connCtx, u.ID)

	// Register with the Hub so lifecycle can force-disconnect / push
	// frames. Unregister fires when serve() returns (either side closed).
	if h.hub != nil {
		h.hub.Register(u.ID, browser)
		defer h.hub.Unregister(u.ID, browser)
	}

	errCh := make(chan error, 2)
	// browser → upstream uses the peeking variant so it can bump
	// last_message_at on user `run` requests. upstream → browser passes
	// frames opaquely.
	go func() { errCh <- copyBrowserToUpstream(connCtx, upstream, browser, h, u.ID) }()
	go func() { errCh <- copyMessages(connCtx, browser, upstream) }()
	<-errCh
	cancel()

	// Drain second goroutine so it's gone before we return (and the
	// deferred Close calls fire). Bounded; we don't block forever.
	select {
	case <-errCh:
	case <-time.After(shutdownGrace):
		h.log.Warn("proxy teardown timed out", "user_id", u.ID)
	}
}

// copyMessages reads one WS message at a time from src and writes it to dst,
// preserving MessageType. Returns when src.Read or dst.Write errors.
func copyMessages(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		msgType, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, msgType, data); err != nil {
			return err
		}
	}
}

// copyBrowserToUpstream is copyMessages plus a peek for `{"type":"run"}`
// frames — those count as "actual user messages" and bump
// last_message_at. Other types (get_messages, context_stats, stop, etc.)
// are not user-sent content; they pass through opaquely without bumping.
// The peek is cheap (single JSON unmarshal into a 1-field struct) and
// errors are silently ignored: a malformed frame is upstream's problem
// to reject.
func copyBrowserToUpstream(ctx context.Context, upstream, browser *websocket.Conn, h *handler, userID string) error {
	for {
		msgType, data, err := browser.Read(ctx)
		if err != nil {
			return err
		}
		if msgType == websocket.MessageText && isRunRequest(data) {
			h.bumpMessage(ctx, userID)
		}
		if err := upstream.Write(ctx, msgType, data); err != nil {
			return err
		}
	}
}

// runProbe is the minimal shape the peek needs. Sharing this struct
// across calls would require sync; allocating per frame is cheap enough
// (no map / slice fields) that we just declare it locally below.
type runProbe struct {
	Type string `json:"type"`
}

func isRunRequest(data []byte) bool {
	var p runProbe
	if err := json.Unmarshal(data, &p); err != nil {
		return false
	}
	return p.Type == "run"
}

// lastActiveTicker periodically refreshes users.last_active_at while the WS
// is open. Each tick uses a detached context with a short timeout so a slow
// DB doesn't block the proxy loop.
func (h *handler) lastActiveTicker(ctx context.Context, userID string) {
	t := time.NewTicker(lastActiveTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.bumpActive(ctx, userID)
		}
	}
}

func (h *handler) bumpActive(ctx context.Context, userID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := h.store.UpdateLastActive(updateCtx, userID, time.Now().UTC().Unix()); err != nil {
		h.log.Warn("UpdateLastActive failed", "user_id", userID, "err", err)
	}
}

// bumpMessage bumps last_message_at — fires on every `run` request the
// browser sends. Drives the compact-on-idle lifecycle; explicitly does
// NOT fire on the WS keepalive ticker so a "tab left open" user still
// rolls into the cold-and-compacted state at 60 min.
func (h *handler) bumpMessage(ctx context.Context, userID string) {
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := h.store.UpdateLastMessage(updateCtx, userID, time.Now().UTC().Unix()); err != nil {
		h.log.Warn("UpdateLastMessage failed", "user_id", userID, "err", err)
	}
}

// dialUpstreamWithRecovery tries the upstream WS dial. On failure with
// a provisioner configured, it best-effort EnsureRunning's the user's
// container (covers the idle-GC'd-or-crashed case) and retries the
// dial once. Keeps the browser side alive throughout — the editor's
// WS reconnect path used to nuke the cookie when the upstream died,
// which was poor UX for transient drops.
//
// Returns the upstream conn on success, or the most recent dial error.
func (h *handler) dialUpstreamWithRecovery(parent context.Context, u *store.User, upstreamURL string) (*websocket.Conn, error) {
	dial := func() (*websocket.Conn, error) {
		ctx, cancel := context.WithTimeout(parent, upstreamDialTimeout)
		defer cancel()
		c, _, err := websocket.Dial(ctx, upstreamURL, nil)
		return c, err
	}
	c, err := dial()
	if err == nil {
		return c, nil
	}
	if h.prov == nil {
		return nil, err
	}
	h.log.Info("upstream dial refused; waking container then retrying",
		"user_id", u.ID, "first_err", err)
	// Generous timeout — EnsureRunning includes container start + the
	// readiness probe (vite warmup). Detached from request context so
	// a slow browser-side timeout doesn't kill the wake mid-flight.
	wakeCtx, wakeCancel := context.WithTimeout(context.Background(), ensureOnDialTimeout)
	defer wakeCancel()
	if errEnsure := h.prov.EnsureRunning(wakeCtx, u.ID); errEnsure != nil {
		h.log.Warn("EnsureRunning during ws-dial recovery failed",
			"user_id", u.ID, "err", errEnsure)
		return nil, err // return ORIGINAL dial error; ensure error is logged
	}
	return dial()
}
