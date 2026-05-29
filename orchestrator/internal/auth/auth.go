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
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/sandboxstatus"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// Cookie + token + validation constants. Centralising them keeps future
// edits (cookie name, token length, etc.) to a single point.
const (
	CookieName     = "homa_session"
	cookieMaxAge   = 30 * 24 * time.Hour
	sessionTokenBytes = 32 // 64 hex chars on the wire
	userIDBytes    = 4     // 8 hex chars on the wire
	// nousSessionIDBytes — 8 hex chars matches the format nous itself uses
	// when it auto-generates session ids (uuid[:8]). Keeps logs / on-disk
	// session dirs visually consistent across host-generated and
	// nous-generated ids.
	nousSessionIDBytes = 4
	minPasswordLen = 8
	bcryptCost     = bcrypt.DefaultCost

	// ensureRunningBgTimeout caps how long the background goroutine
	// that runs EnsureRunning at login will wait. Generous so a slow
	// first-boot (npm install + vite warmup) has room; the editor's
	// loading screen shows the user *something is happening* during
	// this window.
	ensureRunningBgTimeout = 3 * time.Minute

	// applicationMinChars / applicationMaxChars bound each of the three
	// signup application essay fields. Min keeps the operator from
	// being flooded with "asdf" submissions; max keeps the DB row
	// reasonable.
	applicationMinChars = 20
	applicationMaxChars = 4000
)

// friendlyFailureMessage maps an EnsureRunning error to a user-facing
// hint that the editor's "sandbox failed" screen renders verbatim.
// Heuristic — match common keywords. Defaults to a generic message.
func friendlyFailureMessage(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "readiness") || strings.Contains(s, "timeout"):
		// Most commonly: entrypoint exited before vite started. Top
		// suspect is missing/expired Anthropic credentials (entrypoint
		// precondition). Surface that as the actionable suggestion.
		return "Sandbox did not become ready in time. " +
			"Common cause: missing/expired Anthropic credentials. " +
			"Operator: run `claude login` on the host, then `homa reload <userid>`."
	default:
		return "Sandbox failed to start: " + s
	}
}

// usernamePattern is the strict charset/length constraint enforced at
// signup. Lowercase ascii letters, digits, underscore; 3-32 chars. Keeps
// usernames URL-safe (so `/forum/by/<username>` routes never need
// escaping) and free of look-alike Unicode tricks.
var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// Service holds the dependencies shared by all auth handlers.
type Service struct {
	store          *store.Store
	prov           provision.Provisioner
	secureCookies  bool
	previewBaseURL string
	sbStatus       *sandboxstatus.Tracker // nil → no async tracking; login stays sync
	log            *slog.Logger
}

// New constructs a Service. previewBaseURL may be empty (no preview_url in /me
// responses); secureCookies controls the Secure attribute on the cookie.
// sbStatus may be nil — pass a tracker to enable async EnsureRunning at login
// with status polling via /me/sandbox.
func New(s *store.Store, p provision.Provisioner, secureCookies bool, previewBaseURL string, sbStatus *sandboxstatus.Tracker, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:          s,
		prov:           p,
		secureCookies:  secureCookies,
		previewBaseURL: previewBaseURL,
		sbStatus:       sbStatus,
		log:            log,
	}
}

// CORSWrapper is the optional middleware Register wraps GET /me with.
// Matches the shape of cors.Policy.Middleware so the auth package
// doesn't have to import internal/cors (which would invert the
// dependency direction; cors is a leaf).
type CORSWrapper func(http.Handler) http.Handler

// Register binds the four JSON endpoints onto mux. /me is the only one
// that goes through the cookie middleware AND the only one that opts
// into CORS (user's iframe-rendered sites need to call it cross-origin
// to check auth status). corsWrap may be nil → CORS off for /me.
//
// signup/login/logout stay first-party to the editor SPA — they're
// served same-origin and don't need CORS.
func (s *Service) Register(mux *http.ServeMux, corsWrap CORSWrapper) {
	mux.HandleFunc("POST /signup", s.Signup)
	mux.HandleFunc("POST /login", s.Login)
	mux.HandleFunc("POST /logout", s.Logout)

	meHandler := s.RequireAuth(http.HandlerFunc(s.Me))
	// /me/sandbox returns the current bring-up status of the caller's
	// sandbox so the editor can show a loading / failed screen instead
	// of a silent hang or a confusing WS-disconnected state. Same auth
	// gate; same CORS wrap as /me.
	sandboxHandler := s.RequireAuth(http.HandlerFunc(s.SandboxStatus))
	if corsWrap != nil {
		meHandler = corsWrap(meHandler)
		sandboxHandler = corsWrap(sandboxHandler)
		mux.Handle("OPTIONS /me", meHandler)
		mux.Handle("OPTIONS /me/sandbox", sandboxHandler)
	}
	mux.Handle("GET /me", meHandler)
	mux.Handle("GET /me/sandbox", sandboxHandler)
}

// --- request / response shapes ---

type signupReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Username string `json:"username"`
	// Application essay fields — operator reads via `homa review <userid>`
	// to inform manual approval. All three required.
	JoinReason      string `json:"join_reason"`
	MysteryInterest string `json:"mystery_interest"`
	Background      string `json:"background"`
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userIDResp struct {
	UserID string `json:"user_id"`
}

// signupResp signals "application submitted; no cookie issued" to the
// editor. Pending is always true here — signup never auto-approves.
type signupResp struct {
	UserID  string `json:"user_id"`
	Pending bool   `json:"pending"`
}

type meResp struct {
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	Username      string `json:"username"`
	IsAdmin       bool   `json:"is_admin"`
	PreviewURL    string `json:"preview_url"`
	NousSessionID string `json:"nous_session_id"`
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
	req.Username = strings.TrimSpace(strings.ToLower(req.Username))
	if !usernamePattern.MatchString(req.Username) {
		writeError(w, http.StatusBadRequest,
			"username must be 3-32 chars, lowercase a-z / 0-9 / underscore")
		return
	}
	// Application fields. Required + minimum length so operators
	// receive thoughtful answers; cap to a sane length to avoid spam.
	req.JoinReason = strings.TrimSpace(req.JoinReason)
	req.MysteryInterest = strings.TrimSpace(req.MysteryInterest)
	req.Background = strings.TrimSpace(req.Background)
	for _, f := range []struct {
		name, val string
	}{
		{"join_reason", req.JoinReason},
		{"mystery_interest", req.MysteryInterest},
		{"background", req.Background},
	} {
		if l := len(f.val); l < applicationMinChars {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("%s must be at least %d characters (got %d)", f.name, applicationMinChars, l))
			return
		}
		if len(f.val) > applicationMaxChars {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("%s must be at most %d characters", f.name, applicationMaxChars))
			return
		}
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
	// Generate the pinned nous session id at signup. Stored in users row,
	// sent in Hello on every WS connect — sandbox-side nous creates the
	// session lazily on first contact. Decouples session identity from
	// connection timing → no findUnlockedSession races.
	nousSessionID, err := randomHex(nousSessionIDBytes)
	if err != nil {
		s.log.Error("nous session id generation failed", "err", err)
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
		"container", prov.ContainerName, "preview_url", prov.PreviewURL,
		"nous_session_id", nousSessionID)

	now := time.Now().UTC().Unix()
	u := store.User{
		ID:               userID,
		Email:            req.Email,
		PasswordHash:     string(hash),
		Name:             req.Name,
		Username:         req.Username,
		JoinReason:       req.JoinReason,
		MysteryInterest:  req.MysteryInterest,
		Background:       req.Background,
		Approved:         false, // operator runs `homa approve <userid>` to flip
		BranchName:       prov.BranchName,
		WorktreePath:     prov.WorktreePath,
		ContainerName:    prov.ContainerName,
		NousPort:         prov.NousPort,
		PreviewPort:      prov.PreviewPort,
		PreviewServePort: prov.PreviewServePort,
		NousSessionID:    nousSessionID,
		CreatedAt:        now,
		LastActiveAt:     now,
	}
	if err := s.store.CreateUser(r.Context(), u); err != nil {
		if store.IsEmailUniqueViolation(err) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		if store.IsUsernameUniqueViolation(err) {
			writeError(w, http.StatusConflict, "username already taken")
			return
		}
		s.log.Error("create user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// NO cookie at signup. Account is in pending-approval state; user
	// must wait for the operator to run `homa approve <userid>` before
	// they can log in. Editor sees the {pending:true} response and
	// shows an "application submitted" page instead of redirecting
	// to the editor.
	writeJSON(w, http.StatusOK, signupResp{UserID: userID, Pending: true})
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
	// Application gate. Three states:
	//   approved=1, rejected=0  → login allowed
	//   approved=0, rejected=1  → rejected (terminal; different message)
	//   approved=0, rejected=0  → pending review
	// Rejected check first so flipping rejected from approved produces
	// the rejected message rather than pending.
	if u.Rejected {
		writeError(w, http.StatusForbidden, "your application was not accepted")
		return
	}
	if !u.Approved {
		writeError(w, http.StatusForbidden, "your application is pending review")
		return
	}

	// Bump last_active_at AND last_message_at BEFORE EnsureRunning so the
	// lifecycle can't see a stale timestamp mid-bring-up and stop the
	// freshly-started container. The user has shown intent (valid bcrypt
	// match) — that's the moment of activity we want recorded. Login
	// counts as engagement → it resets the idle-compact clock too.
	// (The WS keepalive ticker only bumps last_active_at; only login and
	// actual messages bump last_message_at.)
	now := time.Now().UTC().Unix()
	if err := s.store.UpdateLastActive(r.Context(), u.ID, now); err != nil {
		s.log.Error("update last_active failed", "err", err)
	}
	if err := s.store.UpdateLastMessage(r.Context(), u.ID, now); err != nil {
		s.log.Error("update last_message failed", "err", err)
	}

	// Bring the user's sandbox back up if the GC stopped it. Was previously
	// synchronous (login hangs up to ReadinessTimeout when the container
	// can't come up — e.g. expired Anthropic credentials). Now async:
	// login returns immediately; editor polls /me/sandbox to know when
	// the sandbox is ready and shows a loading screen meanwhile.
	if s.sbStatus != nil {
		s.sbStatus.MarkStarting(u.ID)
		userID := u.ID
		go func() {
			// Detached context — must outlive the HTTP request. Long
			// timeout (>= readiness probe) so the goroutine doesn't
			// terminate early on a slow first-boot.
			ctx, cancel := context.WithTimeout(context.Background(), ensureRunningBgTimeout)
			defer cancel()
			if err := s.prov.EnsureRunning(ctx, userID); err != nil {
				s.log.Warn("EnsureRunning failed at login (background)",
					"user_id", userID, "err", err)
				s.sbStatus.MarkFailed(userID, friendlyFailureMessage(err))
				return
			}
			s.sbStatus.MarkReady(userID)
		}()
	} else {
		// Legacy path (e.g. tests with nil tracker): keep sync behavior.
		if err := s.prov.EnsureRunning(r.Context(), u.ID); err != nil {
			s.log.Warn("EnsureRunning failed at login; continuing", "user_id", u.ID, "err", err)
		}
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
		UserID:        u.ID,
		Email:         u.Email,
		Username:      u.Username,
		IsAdmin:       u.IsAdmin,
		PreviewURL:    s.previewURLFor(u),
		NousSessionID: u.NousSessionID,
	})
}

// SandboxStatus returns the bring-up state of the caller's sandbox
// (starting / ready / failed). The editor polls this after login and
// shows a loading screen while the background EnsureRunning goroutine
// from /login completes. When no tracker is configured, returns
// {ready} unconditionally (legacy sync behavior).
func (s *Service) SandboxStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if s.sbStatus == nil {
		writeJSON(w, http.StatusOK, sandboxstatus.State{Status: sandboxstatus.StatusReady})
		return
	}
	writeJSON(w, http.StatusOK, s.sbStatus.Get(u.ID))
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
