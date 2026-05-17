package forum

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skipper/homa/orchestrator/internal/auth"
)

// CORSWrapper mirrors auth.CORSWrapper. Keeps forum package importable
// from main.go without forcing it to depend on internal/cors directly.
type CORSWrapper func(http.Handler) http.Handler

// Handler owns the four forum routes. Constructed once at startup,
// registered into the orchestrator's mux behind RequireAuth + CORS.
type Handler struct {
	store *Store
	log   *slog.Logger
}

// New constructs a Handler. log may be nil → slog.Default().
func New(store *Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{store: store, log: log}
}

// Register mounts the four forum routes onto mux. Each goes through:
//   corsWrap → authSvc.RequireAuth → handler
// so OPTIONS preflights short-circuit in CORS (before hitting auth),
// and authenticated GET/POST land at the handler with the user on ctx.
//
// All routes require auth — both reads and writes. Forum sits in the
// authed section of the public site (per the user's spec).
func (h *Handler) Register(mux *http.ServeMux, authSvc *auth.Service, corsWrap CORSWrapper) {
	if corsWrap == nil {
		// Identity wrapper so the same composition works in test rigs.
		corsWrap = func(next http.Handler) http.Handler { return next }
	}
	gated := func(fn http.HandlerFunc) http.Handler {
		return corsWrap(authSvc.RequireAuth(fn))
	}
	mux.Handle("GET /api/forum/topics", gated(h.listTopics))
	mux.Handle("POST /api/forum/topics", gated(h.createTopic))
	mux.Handle("GET /api/forum/topics/{id}/posts", gated(h.listPosts))
	mux.Handle("POST /api/forum/topics/{id}/posts", gated(h.createPost))
	// OPTIONS preflights — same wrap so CORS sees them. The handler
	// underneath never runs because cors.Middleware short-circuits
	// OPTIONS with 204.
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mux.Handle("OPTIONS /api/forum/topics", corsWrap(noop))
	mux.Handle("OPTIONS /api/forum/topics/{id}/posts", corsWrap(noop))
}

// --- request shapes ---

type createTopicReq struct {
	Title string `json:"title"`
}
type createPostReq struct {
	Content string `json:"content"`
}

// --- handlers ---

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request) {
	topics, err := h.store.ListTopics(r.Context())
	if err != nil {
		h.log.Error("forum: list topics", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if topics == nil {
		topics = []Topic{}
	}
	writeJSON(w, http.StatusOK, topics)
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req createTopicReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title required")
		return
	}
	t, err := h.store.CreateTopic(r.Context(), title, u.ID, time.Now().UTC().Unix())
	if err != nil {
		h.log.Error("forum: create topic", "user_id", u.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.log.Info("forum: topic created", "user_id", u.ID, "topic_id", t.ID, "title", title)
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) listPosts(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTopicID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid topic id")
		return
	}
	posts, err := h.store.ListPostsByTopic(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "topic not found")
			return
		}
		h.log.Error("forum: list posts", "topic_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, posts)
}

func (h *Handler) createPost(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	id, ok := parseTopicID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid topic id")
		return
	}
	var req createPostReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		writeError(w, http.StatusBadRequest, "content required")
		return
	}
	p, err := h.store.CreatePost(r.Context(), id, u.ID, content, time.Now().UTC().Unix())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "topic not found")
			return
		}
		h.log.Error("forum: create post", "user_id", u.ID, "topic_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	h.log.Info("forum: post created", "user_id", u.ID, "topic_id", id, "post_id", p.ID)
	writeJSON(w, http.StatusCreated, p)
}

// parseTopicID pulls {id} from the URL path. Returns (0, false) on
// malformed input — callers map to 400.
func parseTopicID(r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
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
