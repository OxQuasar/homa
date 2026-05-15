// Package auth implements signup/login/logout/me HTTP handlers plus the
// cookie-based session middleware. The orchestrator wires these handlers onto
// its mux; everything else (sandbox manager, reverse proxy, editor SPA) lives
// elsewhere and is added in later phases.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// Cookie + token + validation constants. Centralising them keeps future
// edits (cookie name, token length, etc.) to a single point.
const (
	CookieName     = "homa_session"
	cookieMaxAge   = 30 * 24 * time.Hour
	sessionTokenBytes = 32 // 64 hex chars on the wire
	userIDBytes    = 4     // 8 hex chars on the wire
	minPasswordLen = 8
	bcryptCost     = bcrypt.DefaultCost
)

// Service holds the dependencies shared by all auth handlers.
type Service struct {
	store          *store.Store
	prov           provision.Provisioner
	secureCookies  bool
	previewBaseURL string
	log            *slog.Logger
}

// New constructs a Service. previewBaseURL may be empty (no preview_url in /me
// responses); secureCookies controls the Secure attribute on the cookie.
func New(s *store.Store, p provision.Provisioner, secureCookies bool, previewBaseURL string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:          s,
		prov:           p,
		secureCookies:  secureCookies,
		previewBaseURL: previewBaseURL,
		log:            log,
	}
}

// Register binds the four JSON endpoints onto mux. /me is the only one that
// goes through the cookie middleware.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /signup", s.Signup)
	mux.HandleFunc("POST /login", s.Login)
	mux.HandleFunc("POST /logout", s.Logout)
	mux.Handle("GET /me", s.RequireAuth(http.HandlerFunc(s.Me)))
}

// --- request / response shapes ---

type signupReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userIDResp struct {
	UserID string `json:"user_id"`
}

type meResp struct {
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	PreviewURL string `json:"preview_url"`
}

// --- handlers ---

// Signup validates input, hashes the password, provisions sandbox metadata,
// creates the user row, issues a session cookie, and returns the new user_id.
func (s *Service) Signup(w http.ResponseWriter, r *http.Request) {
	var req signupReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("password must be at least %d characters", minPasswordLen))
		return
	}

	// Email-uniqueness precheck. The DB UNIQUE constraint is still the source
	// of truth for the race (handled below via IsEmailUniqueViolation), but
	// checking here avoids running bcrypt + spinning up a worktree/container
	// just to roll it back on the common dup-email path.
	if existing, err := s.store.GetUserByEmail(r.Context(), req.Email); err == nil && existing != nil {
		writeError(w, http.StatusConflict, "email already registered")
		return
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("precheck email lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		s.log.Error("bcrypt failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	userID, err := randomHex(userIDBytes)
	if err != nil {
		s.log.Error("user id generation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	prov, err := s.prov.Provision(r.Context(), userID)
	if err != nil {
		// Error is already logged with full context (user_id, stage, etc.)
		// by the provisioner. Surface the chain to the HTTP client too —
		// this is a single-tenant tool, no info-leak concern, and curl /
		// browser without server-log access becomes debuggable.
		s.log.Error("signup: provision failed", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "provisioning failed: "+err.Error())
		return
	}
	s.log.Info("signup: provisioned",
		"user_id", userID, "email", req.Email,
		"container", prov.ContainerName, "preview_url", prov.PreviewURL)

	now := time.Now().UTC().Unix()
	u := store.User{
		ID:               userID,
		Email:            req.Email,
		PasswordHash:     string(hash),
		Name:             req.Name,
		BranchName:       prov.BranchName,
		WorktreePath:     prov.WorktreePath,
		ContainerName:    prov.ContainerName,
		NousPort:         prov.NousPort,
		PreviewPort:      prov.PreviewPort,
		PreviewServePort: prov.PreviewServePort,
		CreatedAt:        now,
		LastActiveAt:     now,
	}
	if err := s.store.CreateUser(r.Context(), u); err != nil {
		if store.IsEmailUniqueViolation(err) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		s.log.Error("create user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := s.issueCookie(r.Context(), w, r, userID); err != nil {
		s.log.Error("issue cookie failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, userIDResp{UserID: userID})
}

// Login verifies password, calls EnsureRunning on the user's sandbox
// (mvp.md §10 step 2 — best-effort, non-fatal), refreshes last_active_at,
// and issues a fresh cookie.
func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	u, err := s.store.GetUserByEmail(r.Context(), req.Email)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		s.log.Error("lookup user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Bump last_active_at BEFORE EnsureRunning so the GC can't see a stale
	// timestamp mid-bring-up and stop the freshly-started container. The
	// user has shown intent (valid bcrypt match) — that's the moment of
	// "activity" we want recorded. If EnsureRunning later fails, the row
	// is still fresh; GC's IsRunning pre-check makes the eventual Stop a
	// no-op anyway.
	now := time.Now().UTC().Unix()
	if err := s.store.UpdateLastActive(r.Context(), u.ID, now); err != nil {
		s.log.Error("update last_active failed", "err", err)
	}

	// Bring the user's sandbox back up if the GC stopped it. Non-fatal —
	// login still succeeds and a future refresh / WS reconnect will retry.
	if err := s.prov.EnsureRunning(r.Context(), u.ID); err != nil {
		s.log.Warn("EnsureRunning failed at login; continuing", "user_id", u.ID, "err", err)
	}
	if err := s.issueCookie(r.Context(), w, r, u.ID); err != nil {
		s.log.Error("issue cookie failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, userIDResp{UserID: u.ID})
}

// Logout deletes the session row referenced by the cookie and clears the
// cookie on the client. Idempotent: missing cookie still returns 204.
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookieName)
	if err == nil && c.Value != "" {
		_ = s.store.DeleteWebSession(r.Context(), c.Value)
	}
	s.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the current user's id, email, and preview URL. The middleware
// guarantees a user is present on the context.
func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, meResp{
		UserID:     u.ID,
		Email:      u.Email,
		PreviewURL: s.previewURLFor(u),
	})
}

// --- helpers ---

// previewURLFor formats the user's preview URL or returns empty when no
// PreviewBaseURL is configured (e.g. local development without tailscale).
func (s *Service) previewURLFor(u *store.User) string {
	if s.previewBaseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", s.previewBaseURL, u.PreviewServePort)
}

// issueCookie creates a fresh web_sessions row and sets the cookie on the
// response. 30-day expiry per mvp.md §6.
func (s *Service) issueCookie(ctx context.Context, w http.ResponseWriter, r *http.Request, userID string) error {
	token, err := randomHex(sessionTokenBytes)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(cookieMaxAge)
	if err := s.store.CreateWebSession(ctx, token, userID, expiresAt.Unix()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		Expires:  expiresAt,
		MaxAge:   int(cookieMaxAge.Seconds()),
	})
	return nil
}

// clearCookie emits a cookie with Max-Age=0 to delete the session client-side.
func (s *Service) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure(r),
		MaxAge:   -1,
	})
}

// cookieSecure resolves the Secure attribute. We use the OR of (a) config and
// (b) whether the request itself arrived over TLS, so even with the config
// override off we still mark cookies Secure when actually served over HTTPS.
func (s *Service) cookieSecure(r *http.Request) bool {
	if s.secureCookies {
		return true
	}
	return r != nil && r.TLS != nil
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
