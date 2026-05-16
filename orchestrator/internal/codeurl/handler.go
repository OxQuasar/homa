// Package codeurl serves GET /code-url — returns the per-user code-server
// URL the editor opens for "Open in VS Code". Cookie-gated; URL embeds a
// one-shot tkn param so code-server auto-logs-in.
//
// Split from the codeserver/ package to break an import cycle: provision
// imports codeserver (for PasswordFor); putting the HTTP handler in
// codeserver would close the loop auth → codeserver → auth.
package codeurl

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/codeserver"
)

// urlResp is the JSON shape returned by GET /code-url. enabled=false
// when the feature is disabled OR the user's code_server_port hasn't
// been allocated yet (existing user pre-backfill); the editor's UI
// hides the "Open VS Code" button in that case.
type urlResp struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

// Handler serves GET /code-url.
type Handler struct {
	host   string
	secret []byte
	log    *slog.Logger
}

// NewHandler constructs the handler.
//   host:   external hostname (no scheme, no port) — orchestrator's
//           PreviewBaseURL stripped of scheme.
//   secret: nil/empty → feature off; otherwise the master secret used
//           to derive per-user passwords.
//   log:    nil → slog.Default().
func NewHandler(host string, secret []byte, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{host: host, secret: secret, log: log}
}

// Register mounts GET /code-url on mux behind authSvc.RequireAuth.
func (h *Handler) Register(mux *http.ServeMux, authSvc *auth.Service) {
	mux.Handle("GET /code-url", authSvc.RequireAuth(http.HandlerFunc(h.serve)))
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	resp := urlResp{Enabled: false}
	if len(h.secret) > 0 && u.CodeServerServePort > 0 && h.host != "" {
		pw := codeserver.PasswordFor(h.secret, u.ID)
		resp.URL = codeserver.URLFor(h.host, u.CodeServerServePort, pw, "/workspace")
		resp.Enabled = true
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
