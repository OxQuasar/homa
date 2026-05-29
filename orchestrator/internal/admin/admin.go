// Package admin serves the admin-only API consumed by the editor's
// /admin SPA route.
//
//   GET  /api/admin/users                  full list with status + essays
//   POST /api/admin/users/{id}/approve     flip the gate to approved
//   POST /api/admin/users/{id}/reject      mark application rejected
//
// All routes pass through auth.RequireAdmin (which itself wraps
// RequireAuth → IsAdmin check). Admins are bootstrapped via the
// `homa promote <userid>` CLI; there is no UI to grant admin.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// CORSWrapper mirrors auth.CORSWrapper / forum.CORSWrapper.
type CORSWrapper func(http.Handler) http.Handler

// Handler owns the admin routes. Constructed once at startup.
type Handler struct {
	store *store.Store
	log   *slog.Logger
}

func New(s *store.Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{store: s, log: log}
}

// Register mounts the admin endpoints behind RequireAdmin + CORS.
// corsWrap may be nil (identity wrapper used by test rigs).
func (h *Handler) Register(mux *http.ServeMux, authSvc *auth.Service, corsWrap CORSWrapper) {
	if corsWrap == nil {
		corsWrap = func(next http.Handler) http.Handler { return next }
	}
	gated := func(fn http.HandlerFunc) http.Handler {
		return corsWrap(authSvc.RequireAdmin(fn))
	}
	mux.Handle("GET /api/admin/users", gated(h.listUsers))
	mux.Handle("POST /api/admin/users/{id}/approve", gated(h.approveUser))
	mux.Handle("POST /api/admin/users/{id}/reject", gated(h.rejectUser))

	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("OPTIONS /api/admin/users", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/users/{id}/approve", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/users/{id}/reject", corsWrap(noop))
}

// AdminUserRow is the wire shape the admin UI consumes. Includes the
// fields needed to render a list + decide on approval (essays).
// Deliberately excludes password hash, secrets, container internals,
// etc. — only what the admin needs to see.
type AdminUserRow struct {
	UserID          string `json:"user_id"`
	Email           string `json:"email"`
	Username        string `json:"username"`
	Name            string `json:"name,omitempty"`
	JoinReason      string `json:"join_reason"`
	MysteryInterest string `json:"mystery_interest"`
	Background      string `json:"background"`
	CreatedAt       int64  `json:"created_at"`
	// Three-state derived from approved/rejected:
	//   "pending" | "approved" | "rejected"
	Status  string `json:"status"`
	IsAdmin bool   `json:"is_admin"`
}

func userStatus(u *store.User) string {
	if u.Rejected {
		return "rejected"
	}
	if u.Approved {
		return "approved"
	}
	return "pending"
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB().QueryContext(r.Context(), `SELECT id FROM users ORDER BY created_at DESC`)
	if err != nil {
		h.log.Error("admin: list ids", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			h.log.Error("admin: scan id", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		ids = append(ids, id)
	}
	out := make([]AdminUserRow, 0, len(ids))
	for _, id := range ids {
		u, err := h.store.GetUserByID(r.Context(), id)
		if err != nil {
			h.log.Warn("admin: user lookup", "id", id, "err", err)
			continue
		}
		out = append(out, AdminUserRow{
			UserID:          u.ID,
			Email:           u.Email,
			Username:        u.Username,
			Name:            u.Name,
			JoinReason:      u.JoinReason,
			MysteryInterest: u.MysteryInterest,
			Background:      u.Background,
			CreatedAt:       u.CreatedAt,
			Status:          userStatus(u),
			IsAdmin:         u.IsAdmin,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) approveUser(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "approve")
}

func (h *Handler) rejectUser(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "reject")
}

// setStatus runs the SetApproved or SetRejected store call based on
// `action`, logs, and returns the updated user row to the caller.
// Centralised so the two handlers stay identical.
//
// reject additionally nukes the target's web_sessions — without this
// step, an already-logged-in user keeps their cookie working until it
// expires (30 days). RequireAuth's gate check (introduced alongside
// this revoke) is the belt; deleting sessions is the suspenders.
func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, action string) {
	actor, _ := auth.UserFromContext(r.Context()) // RequireAdmin guarantees presence
	targetID := r.PathValue("id")
	if targetID == "" {
		writeError(w, http.StatusBadRequest, "missing user id")
		return
	}
	ctx := r.Context()
	var err error
	switch action {
	case "approve":
		err = h.store.SetApproved(ctx, targetID, true)
	case "reject":
		err = h.store.SetRejected(ctx, targetID, true)
	default:
		// Programmer error — unreachable.
		writeError(w, http.StatusInternalServerError, "unknown action")
		return
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("admin: "+action, "user_id", targetID, "err", err)
		writeError(w, http.StatusInternalServerError, action+" failed")
		return
	}
	if action == "reject" {
		// Best-effort: failure to nuke sessions doesn't fail the request
		// (RequireAuth's gate check still locks out the user). Log loud
		// so we'd notice if it became chronic.
		if n, err := h.store.DeleteWebSessionsByUser(ctx, targetID); err != nil {
			h.log.Warn("admin: reject: revoke sessions failed",
				"target_user_id", targetID, "err", err)
		} else if n > 0 {
			h.log.Info("admin: reject: revoked active sessions",
				"target_user_id", targetID, "count", n)
		}
	}
	h.log.Info("admin: "+action+"d user",
		"admin_user_id", actor.ID, "target_user_id", targetID)

	// Return the freshly-updated row so the UI can refresh inline.
	u, err := h.store.GetUserByID(ctx, targetID)
	if err != nil {
		// Already persisted — just return ok with no body if the
		// follow-up read fails. Unlikely; log loud.
		h.log.Warn("admin: post-action GetUserByID failed", "id", targetID, "err", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, AdminUserRow{
		UserID:          u.ID,
		Email:           u.Email,
		Username:        u.Username,
		Name:            u.Name,
		JoinReason:      u.JoinReason,
		MysteryInterest: u.MysteryInterest,
		Background:      u.Background,
		CreatedAt:       u.CreatedAt,
		Status:          userStatus(u),
		IsAdmin:         u.IsAdmin,
	})
}

// Helper accessor for the store's *sql.DB — kept local so admin doesn't
// have to depend on store.Store.DB() existing. (It does; we use it.)
var _ = context.Background // keep context import; QueryContext above uses r.Context()

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
