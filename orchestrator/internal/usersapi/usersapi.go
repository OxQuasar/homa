// Package usersapi serves public "people directory" endpoints — the
// authenticated read-only views of the users table. Separate from
// internal/auth (which owns identity verbs: signup, login, logout, me)
// and from internal/forum (forum content). Lives here so future
// public-user endpoints — e.g. GET /api/users/<username>/topics —
// have a natural home.
//
// Phase 3 ships just one endpoint:
//
//   GET /api/users  → [{user_id, username, created_at}, …]
//
// Auth-required; CORS-wrapped. Emails are deliberately NOT exposed —
// directory listing carries only public identifiers (username is what
// the forum already shows).
package usersapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// CORSWrapper mirrors auth.CORSWrapper / forum.CORSWrapper. Same shape
// so main.go can pass cors.Policy.Middleware uniformly across packages.
type CORSWrapper func(http.Handler) http.Handler

// UserSummary is the wire shape returned in the directory listing.
// Intentionally excludes email + container metadata + port allocations.
type UserSummary struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	CreatedAt int64  `json:"created_at"`
}

// Handler holds the dependencies; constructed once at startup.
type Handler struct {
	store *store.Store
	log   *slog.Logger
}

// New constructs a Handler.
func New(s *store.Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{store: s, log: log}
}

// Register mounts GET /api/users (and its OPTIONS preflight) onto mux.
// corsWrap may be nil → identity wrapper (used by test rigs).
func (h *Handler) Register(mux *http.ServeMux, authSvc *auth.Service, corsWrap CORSWrapper) {
	if corsWrap == nil {
		corsWrap = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /api/users", corsWrap(authSvc.RequireAuth(http.HandlerFunc(h.list))))
	mux.Handle("OPTIONS /api/users", corsWrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	// Direct query on the shared DB: we want a minimal projection that
	// excludes the empty-username placeholder rows (legacy pre-feature
	// users that haven't been backfilled yet, or future deactivated
	// accounts).
	rows, err := h.store.DB().QueryContext(r.Context(), `
		SELECT id, username, created_at FROM users
		WHERE username != ''
		ORDER BY created_at ASC`)
	if err != nil {
		h.log.Error("usersapi: list query", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.UserID, &u.Username, &u.CreatedAt); err != nil {
			h.log.Error("usersapi: scan", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("usersapi: rows.Err", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
