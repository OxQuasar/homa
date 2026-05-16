// hub.go — process-wide registry of active browser↔sandbox WS proxy
// connections, keyed by user id. Two purposes:
//
//   1. Force-disconnect: lifecycle's idle-compact step needs to release
//      nous's session lock (held by the browser's open WS) before it can
//      take the lock itself and run the compaction.
//   2. Server-push to the browser: lifecycle sends synthetic events
//      (e.g. "idle compaction in 60s") to live browser tabs so the UI can
//      warn the user before their container gets compacted-and-stopped.
//
// A user can have multiple concurrent connections (two browser tabs); all
// of them are tracked under the same userID and acted on together.
//
// The Hub itself is registered globally via package-level New + Default()
// so tests can build their own. Locking is fine-grained: a single mutex
// guards the userID → conns map; per-conn writes happen without holding
// the mutex so a slow socket can't stall others.

package proxy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub holds the per-user set of live browser WS connections.
type Hub struct {
	mu  sync.Mutex
	all map[string]map[*websocket.Conn]struct{}
	log *slog.Logger
}

// NewHub creates an empty Hub. log may be nil → slog.Default().
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		all: make(map[string]map[*websocket.Conn]struct{}),
		log: log,
	}
}

// Register records a browser conn under userID. Idempotent.
func (h *Hub) Register(userID string, c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.all[userID]
	if !ok {
		set = make(map[*websocket.Conn]struct{})
		h.all[userID] = set
	}
	set[c] = struct{}{}
}

// Unregister removes a conn, cleaning the user's slot if it becomes empty.
// Safe to call repeatedly with the same conn.
func (h *Hub) Unregister(userID string, c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.all[userID]
	if !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.all, userID)
	}
}

// HasUser reports whether at least one browser WS is currently registered
// for userID. Cheap snapshot — used by lifecycle to decide whether a
// disconnect step is actually needed before compaction.
func (h *Hub) HasUser(userID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.all[userID]
	return ok
}

// SendToUser writes raw text frame to every browser WS registered for
// userID. Returns the number of conns the frame was delivered to.
// Per-conn writes use a short timeout so a slow client doesn't stall the
// caller (lifecycle ticker should never block on a wedged socket).
func (h *Hub) SendToUser(userID string, raw []byte) int {
	h.mu.Lock()
	conns := h.all[userID]
	// Snapshot under lock so iteration outside the lock can't race with
	// Unregister.
	snap := make([]*websocket.Conn, 0, len(conns))
	for c := range conns {
		snap = append(snap, c)
	}
	h.mu.Unlock()

	delivered := 0
	for _, c := range snap {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
			h.log.Warn("hub send failed", "user_id", userID, "err", err)
			cancel()
			continue
		}
		cancel()
		delivered++
	}
	return delivered
}

// Disconnect closes every browser WS registered for userID. Used by
// lifecycle before opening its own connection to nous (which would
// otherwise be refused with "session locked by another connection").
// Returns the number of conns closed. The deferred Unregister inside
// each proxy serve() handles map cleanup as the conn.Close path
// surfaces in copyMessages.
func (h *Hub) Disconnect(userID string, reason string) int {
	h.mu.Lock()
	conns := h.all[userID]
	snap := make([]*websocket.Conn, 0, len(conns))
	for c := range conns {
		snap = append(snap, c)
	}
	h.mu.Unlock()

	for _, c := range snap {
		// StatusGoingAway is the polite "I'm closing this on my end"
		// signal. Browsers handle it gracefully; the editor's onClose
		// flips wsStatus to 'closed' and the user can reconnect later.
		if err := c.Close(websocket.StatusGoingAway, reason); err != nil {
			h.log.Warn("hub disconnect close failed", "user_id", userID, "err", err)
		}
	}
	return len(snap)
}
