package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/skipper/homa/orchestrator/internal/store"
)

// ctxKey is unexported so callers must go through UserFromContext.
type ctxKey int

const userCtxKey ctxKey = iota

// lookupUser resolves the homa_session cookie on r to a *store.User. Returns
// nil + nil error if the cookie is missing or invalid. A non-nil error means
// the store itself failed and the caller should 500.
//
// Side-effect: if the cookie exists but the session is expired or orphaned,
// the row is deleted and the cookie is cleared on w. Pass a nil w to suppress
// the cookie clear (useful for read-only probes like static.Register).
func (s *Service) lookupUser(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, nil
	}

	ws, err := s.store.GetWebSession(r.Context(), c.Value)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if time.Now().UTC().Unix() >= ws.ExpiresAt {
		_ = s.store.DeleteWebSession(r.Context(), c.Value)
		if w != nil {
			s.clearCookie(w, r)
		}
		return nil, nil
	}

	u, err := s.store.GetUserByID(r.Context(), ws.UserID)
	if errors.Is(err, store.ErrNotFound) {
		_ = s.store.DeleteWebSession(r.Context(), c.Value)
		if w != nil {
			s.clearCookie(w, r)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// RequireAuth resolves the homa_session cookie to a user and stashes it on
// the request context. Missing / expired / unknown tokens → 401 JSON.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.lookupUser(w, r)
		if err != nil {
			s.log.Error("session lookup failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if u == nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// LookupCookie is a read-only variant of RequireAuth — returns the user if a
// valid session cookie is present, otherwise nil. Never writes to w; never
// returns 401. Intended for routes that pick a destination based on whether
// the visitor is logged in (e.g. static.Register's "/" → /editor vs /login).
func (s *Service) LookupCookie(r *http.Request) *store.User {
	u, err := s.lookupUser(nil, r)
	if err != nil {
		s.log.Error("cookie lookup failed", "err", err)
	}
	return u
}

// UserFromContext returns the authenticated user attached by RequireAuth.
func UserFromContext(ctx context.Context) (*store.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*store.User)
	return u, ok
}

// RequireAdmin chains RequireAuth + an IsAdmin check. Non-admins get
// 403 (not 401) — they're authenticated, just not authorized. Mounted
// in front of every /api/admin/* route.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		if !u.IsAdmin {
			writeError(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r)
	}))
}
