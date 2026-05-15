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
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/skipper/homa/orchestrator/internal/auth"
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
)

// Register mounts the cookie-gated /ws reverse proxy onto mux. The auth
// service supplies both RequireAuth and the user-on-context contract.
func Register(mux *http.ServeMux, st *store.Store, authSvc *auth.Service, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	h := &handler{store: st, log: log}
	mux.Handle("GET /ws", authSvc.RequireAuth(http.HandlerFunc(h.serve)))
}

type handler struct {
	store *store.Store
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
	dialCtx, dialCancel := context.WithTimeout(r.Context(), upstreamDialTimeout)
	defer dialCancel()

	upstream, _, err := websocket.Dial(dialCtx, upstreamURL, nil)
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

	errCh := make(chan error, 2)
	go func() { errCh <- copyMessages(connCtx, browser, upstream) }()
	go func() { errCh <- copyMessages(connCtx, upstream, browser) }()
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
