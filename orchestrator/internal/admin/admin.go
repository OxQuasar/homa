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
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

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
	mux.Handle("GET /api/admin/password-resets", gated(h.listPasswordResets))
	mux.Handle("POST /api/admin/password-resets/{id}/reset", gated(h.resetPassword))
	mux.Handle("POST /api/admin/password-resets/{id}/dismiss", gated(h.dismissPasswordReset))

	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("OPTIONS /api/admin/users", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/users/{id}/approve", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/users/{id}/reject", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/password-resets", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/password-resets/{id}/reset", corsWrap(noop))
	mux.Handle("OPTIONS /api/admin/password-resets/{id}/dismiss", corsWrap(noop))
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
	// Defense against admin self-lockout. The UI already hides the
	// Reject button on admin rows, but the API was curl-able. A
	// rejected admin would lose access to /api/admin/* and need SQL
	// surgery to recover. Approve-self is fine (idempotent).
	if action == "reject" && targetID == actor.ID {
		writeError(w, http.StatusBadRequest, "cannot reject yourself")
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

// --- password reset request handling ------------------------------

// AdminPasswordResetRow is the wire shape for one row in the password
// resets list. Mirrors store.PasswordResetRequest but with JSON tags
// + a derived 'status' field for the UI.
type AdminPasswordResetRow struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	UserID     string `json:"user_id,omitempty"`
	Note       string `json:"note,omitempty"`
	ClientIP   string `json:"client_ip,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	ResolvedAt int64  `json:"resolved_at,omitempty"`
	ResolvedBy string `json:"resolved_by,omitempty"`
	Status     string `json:"status"` // "pending" | "resolved"
}

func rowFromRequest(r store.PasswordResetRequest) AdminPasswordResetRow {
	status := "pending"
	if r.ResolvedAt > 0 {
		status = "resolved"
	}
	return AdminPasswordResetRow{
		ID: r.ID, Email: r.Email, UserID: r.UserID, Note: r.Note,
		ClientIP:   r.ClientIP,
		CreatedAt:  r.CreatedAt,
		ResolvedAt: r.ResolvedAt,
		ResolvedBy: r.ResolvedBy,
		Status:     status,
	}
}

// listPasswordResets returns pending requests + resolved-in-last-30d.
// Older resolved entries are dropped from the UI to keep the list tidy.
const recentResolvedDays = 30

func (h *Handler) listPasswordResets(w http.ResponseWriter, r *http.Request) {
	reqs, err := h.store.ListPasswordResetRequests(r.Context(), recentResolvedDays)
	if err != nil {
		h.log.Error("admin: list password resets", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]AdminPasswordResetRow, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, rowFromRequest(r))
	}
	writeJSON(w, http.StatusOK, out)
}

// PasswordResetResultResp is returned ONCE by the reset endpoint with
// the freshly-generated password. The admin must copy it from the UI
// immediately — the cleartext is never stored anywhere on disk; only
// its bcrypt hash lives in users.password_hash.
type PasswordResetResultResp struct {
	Request     AdminPasswordResetRow `json:"request"`
	NewPassword string                `json:"new_password"`
}

// resetPassword executes the password rotation. Three side-effects:
//  1. UPDATE users.password_hash with bcrypt of a new random password.
//  2. DELETE every web_session for the target user (existing cookies
//     stop working — if the "forgot" was credential compromise, this
//     is the close).
//  3. Mark the request resolved with the acting admin's id.
//
// Returns the cleartext password in the response. Operator copies +
// shares with the user out of band.
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.UserFromContext(r.Context())
	id, ok := parsePathID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	req, err := h.store.GetPasswordResetRequest(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "request not found")
			return
		}
		h.log.Error("admin: get password reset", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest,
			"this request didn't match a user — use Dismiss instead")
		return
	}
	if req.ResolvedAt > 0 {
		writeError(w, http.StatusBadRequest, "request already resolved")
		return
	}

	pw, err := generatePassword(passwordLength)
	if err != nil {
		h.log.Error("admin: generate password", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		h.log.Error("admin: hash password", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.store.UpdatePasswordHash(r.Context(), req.UserID, string(hash)); err != nil {
		h.log.Error("admin: update password hash", "user_id", req.UserID, "err", err)
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	// Belt + suspenders: revoke any active sessions. If forgot was due
	// to credential leak the attacker is locked out immediately.
	if n, err := h.store.DeleteWebSessionsByUser(r.Context(), req.UserID); err != nil {
		h.log.Warn("admin: revoke sessions on reset", "user_id", req.UserID, "err", err)
	} else if n > 0 {
		h.log.Info("admin: revoked sessions during reset",
			"user_id", req.UserID, "count", n)
	}
	now := time.Now().UTC().Unix()
	if err := h.store.ResolvePasswordResetRequest(r.Context(), id, actor.ID, now); err != nil {
		h.log.Warn("admin: mark request resolved", "id", id, "err", err)
	}

	h.log.Info("admin: password reset",
		"admin_user_id", actor.ID, "target_user_id", req.UserID, "request_id", id)

	// Re-read the resolved row so the UI gets fresh state.
	updated, err := h.store.GetPasswordResetRequest(r.Context(), id)
	if err != nil {
		h.log.Warn("admin: post-reset GetPasswordResetRequest", "err", err)
		// Still return the password — the rotation already succeeded.
		writeJSON(w, http.StatusOK, PasswordResetResultResp{NewPassword: pw})
		return
	}
	writeJSON(w, http.StatusOK, PasswordResetResultResp{
		Request:     rowFromRequest(*updated),
		NewPassword: pw,
	})
}

// dismissPasswordReset marks the request resolved without changing the
// password. For spam / phantom-email rows the admin doesn't want to act on.
func (h *Handler) dismissPasswordReset(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.UserFromContext(r.Context())
	id, ok := parsePathID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	req, err := h.store.GetPasswordResetRequest(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "request not found")
			return
		}
		h.log.Error("admin: get password reset", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if req.ResolvedAt > 0 {
		writeError(w, http.StatusBadRequest, "request already resolved")
		return
	}
	now := time.Now().UTC().Unix()
	if err := h.store.ResolvePasswordResetRequest(r.Context(), id, actor.ID, now); err != nil {
		h.log.Error("admin: dismiss", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "dismiss failed")
		return
	}
	h.log.Info("admin: dismissed password reset",
		"admin_user_id", actor.ID, "request_id", id)
	updated, _ := h.store.GetPasswordResetRequest(r.Context(), id)
	writeJSON(w, http.StatusOK, rowFromRequest(*updated))
}

// passwordAlphabet excludes ambiguous characters (0/O, 1/l/I) so the
// admin can dictate / type the generated password without confusion.
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
const passwordLength = 12

// generatePassword returns a cryptographically random password of n
// characters drawn from passwordAlphabet.
func generatePassword(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = passwordAlphabet[int(b)%len(passwordAlphabet)]
	}
	return string(out), nil
}

func parsePathID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
