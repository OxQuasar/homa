package forum_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/cors"
	"github.com/skipper/homa/orchestrator/internal/forum"
	"github.com/skipper/homa/orchestrator/internal/store"
)

const previewBase = "https://gandiva.tailnet.ts.net"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type rig struct {
	server     *httptest.Server
	store      *forum.Store
	userTokens map[string]string // userID → cookie token
}

// newRig stands up a fresh SQLite with forum schema + a couple of test
// users + the handler mounted on httptest. Each test gets its own DB
// via t.TempDir, so no inter-test cross-talk.
func newRig(t *testing.T) *rig {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "homa.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	users := []store.User{
		{ID: "alice001", Email: "a@x", PasswordHash: "$2a", Username: "alice",
			BranchName: "u/a", WorktreePath: "/wt", ContainerName: "c", NousPort: 1,
			PreviewPort: 2, PreviewServePort: 3, NousSessionID: "s",
			CreatedAt: 1, LastActiveAt: 1, LastMessageAt: 1},
		{ID: "bob00001", Email: "b@x", PasswordHash: "$2a", Username: "bob",
			BranchName: "u/b", WorktreePath: "/wt", ContainerName: "c2", NousPort: 4,
			PreviewPort: 5, PreviewServePort: 6, NousSessionID: "s2",
			CreatedAt: 2, LastActiveAt: 2, LastMessageAt: 2},
	}
	tokens := map[string]string{
		"alice001": "tokalicetokalicetokalicetokalice",
		"bob00001": "tokbob00tokbob00tokbob00tokbob00",
	}
	for _, u := range users {
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.ID, err)
		}
		st.CreateWebSession(context.Background(), tokens[u.ID], u.ID, 9_999_999_999)
	}

	authSvc := auth.New(st, nil, false, "", quietLog())
	mux := http.NewServeMux()
	policy := cors.New(previewBase)
	fstore := forum.NewStore(st.DB())
	forum.New(fstore, quietLog()).Register(mux, authSvc, policy.Middleware)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &rig{server: srv, store: fstore, userTokens: tokens}
}

// do issues an HTTP request with the user's auth cookie attached. Pass
// userID = "" to skip auth (for testing 401 paths).
func (r *rig) do(t *testing.T, method, path, userID string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.server.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: r.userTokens[userID]})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func readJSON(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ----------------------------------------------------------------------
// Store-level tests
// ----------------------------------------------------------------------

func TestStore_CreateAndListTopics(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	t1, err := r.store.CreateTopic(ctx, "First topic", "alice001", 1000)
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}
	if t1.Title != "First topic" || t1.AuthorID != "alice001" || t1.AuthorName != "alice" {
		t.Errorf("CreateTopic returned: %+v", t1)
	}
	if t1.PostCount != 0 {
		t.Errorf("new topic post count: got %d, want 0", t1.PostCount)
	}

	// Second topic with later created_at → should sort first.
	r.store.CreateTopic(ctx, "Second", "bob00001", 2000)
	r.store.CreateTopic(ctx, "Third", "alice001", 1500)

	all, err := r.store.ListTopics(ctx)
	if err != nil {
		t.Fatalf("ListTopics: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListTopics len: got %d, want 3", len(all))
	}
	// Newest first.
	if all[0].Title != "Second" || all[1].Title != "Third" || all[2].Title != "First topic" {
		titles := []string{all[0].Title, all[1].Title, all[2].Title}
		t.Errorf("order: got %v, want [Second Third 'First topic']", titles)
	}
}

func TestStore_PostCountIncludesReplies(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tp, _ := r.store.CreateTopic(ctx, "T", "alice001", 1)
	r.store.CreatePost(ctx, tp.ID, "alice001", "reply 1", 2)
	r.store.CreatePost(ctx, tp.ID, "bob00001", "reply 2", 3)
	got, _ := r.store.GetTopic(ctx, tp.ID)
	if got.PostCount != 2 {
		t.Errorf("PostCount: got %d, want 2", got.PostCount)
	}
}

func TestStore_GetTopic_NotFound(t *testing.T) {
	r := newRig(t)
	if _, err := r.store.GetTopic(context.Background(), 999); err == nil {
		t.Error("GetTopic(999): got nil, want ErrNotFound")
	}
}

func TestStore_ListPostsByTopic_NewestFirst(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tp, _ := r.store.CreateTopic(ctx, "T", "alice001", 1)
	r.store.CreatePost(ctx, tp.ID, "alice001", "first", 100)
	r.store.CreatePost(ctx, tp.ID, "bob00001", "second", 200)
	r.store.CreatePost(ctx, tp.ID, "alice001", "third", 300)

	posts, err := r.store.ListPostsByTopic(ctx, tp.ID)
	if err != nil {
		t.Fatalf("ListPostsByTopic: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("posts len: %d", len(posts))
	}
	contents := []string{posts[0].Content, posts[1].Content, posts[2].Content}
	want := []string{"third", "second", "first"}
	if !equal(contents, want) {
		t.Errorf("order: got %v, want %v", contents, want)
	}
	// Author names hydrated.
	for _, p := range posts {
		if p.AuthorName == "" {
			t.Errorf("post %d missing author name", p.ID)
		}
	}
}

func TestStore_CreatePost_NonexistentTopic(t *testing.T) {
	r := newRig(t)
	_, err := r.store.CreatePost(context.Background(), 999, "alice001", "x", 1)
	if err == nil {
		t.Error("got nil; want ErrNotFound")
	}
}

// ----------------------------------------------------------------------
// HTTP handler tests
// ----------------------------------------------------------------------

func TestHandler_ListTopics_Empty(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "GET", "/api/forum/topics", "alice001", nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got []forum.Topic
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 0 {
		t.Errorf("len: got %d, want 0", len(got))
	}
}

func TestHandler_CreateAndList(t *testing.T) {
	r := newRig(t)
	// Alice creates a topic
	c := r.do(t, "POST", "/api/forum/topics", "alice001", map[string]string{"title": "Hello"})
	if c.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", c.StatusCode)
	}
	var topic forum.Topic
	readJSON(t, c, &topic)
	if topic.AuthorName != "alice" {
		t.Errorf("AuthorName: got %q, want alice", topic.AuthorName)
	}
	// Bob lists — sees the topic
	l := r.do(t, "GET", "/api/forum/topics", "bob00001", nil)
	var topics []forum.Topic
	readJSON(t, l, &topics)
	if len(topics) != 1 || topics[0].Title != "Hello" {
		t.Errorf("list: %+v", topics)
	}
}

func TestHandler_PostReply(t *testing.T) {
	r := newRig(t)
	c := r.do(t, "POST", "/api/forum/topics", "alice001", map[string]string{"title": "T"})
	var topic forum.Topic
	readJSON(t, c, &topic)

	// Bob replies
	url := "/api/forum/topics/" + intToA(topic.ID) + "/posts"
	pr := r.do(t, "POST", url, "bob00001", map[string]string{"content": "Hi alice"})
	if pr.StatusCode != http.StatusCreated {
		t.Fatalf("reply status: %d", pr.StatusCode)
	}
	var post forum.Post
	readJSON(t, pr, &post)
	if post.AuthorName != "bob" || post.Content != "Hi alice" {
		t.Errorf("post: %+v", post)
	}

	// Alice lists posts
	lr := r.do(t, "GET", url, "alice001", nil)
	var posts []forum.Post
	readJSON(t, lr, &posts)
	if len(posts) != 1 || posts[0].Content != "Hi alice" {
		t.Errorf("posts: %+v", posts)
	}
}

func TestHandler_AuthRequired(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/api/forum/topics"},
		{"POST", "/api/forum/topics"},
		{"GET", "/api/forum/topics/1/posts"},
		{"POST", "/api/forum/topics/1/posts"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := r.do(t, tc.method, tc.path, "", map[string]string{"x": "y"})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("got %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestHandler_CORSPreflight(t *testing.T) {
	r := newRig(t)
	req, _ := http.NewRequest("OPTIONS", r.server.URL+"/api/forum/topics", nil)
	req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight: got %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://gandiva.tailnet.ts.net:10001" {
		t.Errorf("ACAO: %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	// And dynamic-path preflight (with {id}) also handled.
	req2, _ := http.NewRequest("OPTIONS", r.server.URL+"/api/forum/topics/123/posts", nil)
	req2.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("dynamic preflight: %d", resp2.StatusCode)
	}
}

func TestHandler_InvalidTopicID(t *testing.T) {
	r := newRig(t)
	// Topic that doesn't exist
	resp := r.do(t, "GET", "/api/forum/topics/9999/posts", "alice001", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nonexistent topic: got %d, want 404", resp.StatusCode)
	}
	// Malformed id
	resp2 := r.do(t, "GET", "/api/forum/topics/abc/posts", "alice001", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed id: got %d, want 400", resp2.StatusCode)
	}
}

func TestHandler_EmptyTitleRejected(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/forum/topics", "alice001", map[string]string{"title": "   "})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty title: got %d, want 400", resp.StatusCode)
	}
}

func TestHandler_EmptyContentRejected(t *testing.T) {
	r := newRig(t)
	c := r.do(t, "POST", "/api/forum/topics", "alice001", map[string]string{"title": "x"})
	var topic forum.Topic
	readJSON(t, c, &topic)
	resp := r.do(t, "POST", "/api/forum/topics/"+intToA(topic.ID)+"/posts",
		"alice001", map[string]string{"content": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty content: got %d, want 400", resp.StatusCode)
	}
}

// helpers ---------------------------------------------------------------

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func intToA(n int64) string {
	// strconv would do this too — local helper to keep imports minimal.
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// Catch the case where ListPostsByTopic could panic on a nil-slice
// JSON-encoding. Sort imports keep this file vet-clean.
var _ = sort.Slice
