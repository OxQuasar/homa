package messages

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/skipper/homa/orchestrator/internal/auth"
)

// CORSWrapper mirrors auth.CORSWrapper / forum.CORSWrapper.
type CORSWrapper func(http.Handler) http.Handler

// Handler owns the four DM routes.
type Handler struct {
	store *Store
	log   *slog.Logger
}

func New(s *Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{store: s, log: log}
}

// Register mounts the four routes plus their OPTIONS preflights onto
// mux. Each route: corsWrap → authSvc.RequireAuth → handler.
func (h *Handler) Register(mux *http.ServeMux, authSvc *auth.Service, corsWrap CORSWrapper) {
	if corsWrap == nil {
		corsWrap = func(next http.Handler) http.Handler { return next }
	}
	gated := func(fn http.HandlerFunc) http.Handler {
		return corsWrap(authSvc.RequireAuth(fn))
	}
	mux.Handle("GET /api/messages/conversations", gated(h.listConversations))
	mux.Handle("GET /api/messages/unread-count", gated(h.unreadCount))
	mux.Handle("GET /api/messages/with/{peer}", gated(h.listThread))
	mux.Handle("POST /api/messages/with/{peer}", gated(h.sendMessage))

	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("OPTIONS /api/messages/conversations", corsWrap(noop))
	mux.Handle("OPTIONS /api/messages/unread-count", corsWrap(noop))
	mux.Handle("OPTIONS /api/messages/with/{peer}", corsWrap(noop))
}

// --- request shapes ---

type sendReq struct {
	Content string `json:"content"`
}

// --- handlers ---

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	convos, err := h.store.ListConversations(r.Context(), u.ID)
	if err != nil {
		h.log.Error("messages: list conversations", "user_id", u.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if convos == nil {
		convos = []Conversation{}
	}
	writeJSON(w, http.StatusOK, convos)
}

func (h *Handler) unreadCount(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	n, err := h.store.UnreadCount(r.Context(), u.ID)
	if err != nil {
		h.log.Error("messages: unread count", "user_id", u.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// listThread returns the message history with one peer. Side effect:
// marks all messages from peer→user as read at the same instant. This
// is the natural read-receipt point — "you opened the thread."
func (h *Handler) listThread(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	peer := r.PathValue("peer")
	if peer == "" {
		writeError(w, http.StatusBadRequest, "missing peer id")
		return
	}
	if peer == u.ID {
		writeError(w, http.StatusBadRequest, "cannot message yourself")
		return
	}
	msgs, err := h.store.ListThread(r.Context(), u.ID, peer)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("messages: list thread", "user_id", u.ID, "peer", peer, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Best-effort mark-read; failure here doesn't block the response.
	if n, err := h.store.MarkRead(r.Context(), u.ID, peer, time.Now().UTC().Unix()); err != nil {
		h.log.Warn("messages: mark-read failed", "user_id", u.ID, "peer", peer, "err", err)
	} else if n > 0 {
		h.log.Info("messages: marked read", "user_id", u.ID, "peer", peer, "count", n)
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	peer := r.PathValue("peer")
	if peer == "" {
		writeError(w, http.StatusBadRequest, "missing peer id")
		return
	}
	if peer == u.ID {
		writeError(w, http.StatusBadRequest, "cannot message yourself")
		return
	}
	var req sendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content required")
		return
	}
	msg, err := h.store.CreateMessage(r.Context(), u.ID, peer, content, time.Now().UTC().Unix())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "recipient not found")
			return
		}
		h.log.Error("messages: send", "user_id", u.ID, "peer", peer, "err", err)
		writeError(w, http.StatusInternalServerError, "send failed")
		return
	}
	h.log.Info("messages: sent", "sender_id", u.ID, "recipient_id", peer, "message_id", msg.ID)
	writeJSON(w, http.StatusCreated, msg)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
