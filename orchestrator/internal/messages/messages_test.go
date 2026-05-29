package messages_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/cors"
	"github.com/skipper/homa/orchestrator/internal/messages"
	"github.com/skipper/homa/orchestrator/internal/store"
)

const previewBase = "https://gandiva.tailnet.ts.net"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type rig struct {
	server *httptest.Server
	store  *messages.Store
	tokens map[string]string // userID → cookie token
}

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
			BranchName: "u/a", WorktreePath: "/wt", ContainerName: "c1",
			NousPort: 1, PreviewPort: 2, PreviewServePort: 3, NousSessionID: "s",
			CreatedAt: 1, LastActiveAt: 1, LastMessageAt: 1, Approved: true},
		{ID: "bob00001", Email: "b@x", PasswordHash: "$2a", Username: "bob",
			BranchName: "u/b", WorktreePath: "/wt", ContainerName: "c2",
			NousPort: 4, PreviewPort: 5, PreviewServePort: 6, NousSessionID: "s2",
			CreatedAt: 2, LastActiveAt: 2, LastMessageAt: 2, Approved: true},
		{ID: "carol001", Email: "c@x", PasswordHash: "$2a", Username: "carol",
			BranchName: "u/c", WorktreePath: "/wt", ContainerName: "c3",
			NousPort: 7, PreviewPort: 8, PreviewServePort: 9, NousSessionID: "s3",
			CreatedAt: 3, LastActiveAt: 3, LastMessageAt: 3, Approved: true},
	}
	tokens := map[string]string{
		"alice001": "alicetokalicetokalicetokalicetok",
		"bob00001": "bobtoktokbobtoktokbobtoktokbobto",
		"carol001": "caroltoktokcaroltoktokcaroltokto",
	}
	for _, u := range users {
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.ID, err)
		}
		st.CreateWebSession(context.Background(), tokens[u.ID], u.ID, 9_999_999_999)
	}

	authSvc := auth.New(st, nil, false, "", nil, quietLog())
	mux := http.NewServeMux()
	policy := cors.New(previewBase)
	msgStore := messages.NewStore(st.DB())
	messages.New(msgStore, quietLog()).Register(mux, authSvc, policy.Middleware)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &rig{server: srv, store: msgStore, tokens: tokens}
}

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
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: r.tokens[userID]})
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
// Store
// ----------------------------------------------------------------------

func TestStore_CreateAndListThread(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// Alice → Bob
	_, err := r.store.CreateMessage(ctx, "alice001", "bob00001", "hi bob", 100)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Bob → Alice
	r.store.CreateMessage(ctx, "bob00001", "alice001", "hi alice", 200)
	// Alice → Bob again
	r.store.CreateMessage(ctx, "alice001", "bob00001", "how are you?", 300)

	msgs, err := r.store.ListThread(ctx, "alice001", "bob00001")
	if err != nil {
		t.Fatalf("ListThread: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len: got %d, want 3", len(msgs))
	}
	// Oldest first
	if msgs[0].Content != "hi bob" || msgs[2].Content != "how are you?" {
		t.Errorf("ordering wrong: %+v", msgs)
	}
	// SenderUsername hydrated
	if msgs[1].SenderUsername != "bob" {
		t.Errorf("bob's username: %+v", msgs[1])
	}
	// Symmetric: bob asking for the same thread sees same messages
	msgsBob, _ := r.store.ListThread(ctx, "bob00001", "alice001")
	if len(msgsBob) != 3 {
		t.Errorf("bob's view: len %d", len(msgsBob))
	}
}

func TestStore_MarkReadAndUnreadCount(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.store.CreateMessage(ctx, "bob00001", "alice001", "msg 1", 100)
	r.store.CreateMessage(ctx, "bob00001", "alice001", "msg 2", 200)
	r.store.CreateMessage(ctx, "carol001", "alice001", "from carol", 150)

	n, err := r.store.UnreadCount(ctx, "alice001")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if n != 3 {
		t.Errorf("unread: got %d, want 3", n)
	}
	// Mark Bob's messages as read
	marked, err := r.store.MarkRead(ctx, "alice001", "bob00001", 999)
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if marked != 2 {
		t.Errorf("marked: got %d, want 2", marked)
	}
	// Re-check unread — only Carol's remains
	n2, _ := r.store.UnreadCount(ctx, "alice001")
	if n2 != 1 {
		t.Errorf("after mark: got %d, want 1", n2)
	}
	// Idempotent: marking again is 0 rows affected
	marked2, _ := r.store.MarkRead(ctx, "alice001", "bob00001", 1000)
	if marked2 != 0 {
		t.Errorf("re-mark: got %d, want 0", marked2)
	}
}

func TestStore_ListConversations(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.store.CreateMessage(ctx, "alice001", "bob00001", "hey bob", 100)
	r.store.CreateMessage(ctx, "bob00001", "alice001", "hey alice", 200)
	r.store.CreateMessage(ctx, "carol001", "alice001", "from carol", 150)
	// Most recent: alice ← bob (200).
	convos, err := r.store.ListConversations(ctx, "alice001")
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("len: %d", len(convos))
	}
	// Sorted newest-first
	if convos[0].PeerID != "bob00001" || convos[1].PeerID != "carol001" {
		t.Errorf("order: %+v", convos)
	}
	// Bob's last message preview
	if convos[0].LastPreview != "hey alice" {
		t.Errorf("preview: %q", convos[0].LastPreview)
	}
	// Both should have unread=1 (one msg each from peer)
	for _, c := range convos {
		if c.UnreadCount != 1 {
			t.Errorf("convo %s: unread=%d, want 1", c.PeerID, c.UnreadCount)
		}
	}
}

func TestStore_PreviewTruncation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	r.store.CreateMessage(ctx, "bob00001", "alice001", string(long), 100)
	convos, _ := r.store.ListConversations(ctx, "alice001")
	if len(convos) != 1 {
		t.Fatalf("len: %d", len(convos))
	}
	if len(convos[0].LastPreview) != 120+len("…") {
		t.Errorf("preview len: %d (incl ellipsis)", len(convos[0].LastPreview))
	}
}

func TestStore_CreateMessage_RecipientNotFound(t *testing.T) {
	r := newRig(t)
	_, err := r.store.CreateMessage(context.Background(),
		"alice001", "ghostxxx", "hi", 100)
	if err == nil {
		t.Error("got nil; want ErrNotFound")
	}
}

// ----------------------------------------------------------------------
// HTTP handler
// ----------------------------------------------------------------------

func TestHandler_SendAndList(t *testing.T) {
	r := newRig(t)
	// Alice sends Bob a message
	c := r.do(t, "POST", "/api/messages/with/bob00001", "alice001",
		map[string]string{"content": "Hello Bob"})
	if c.StatusCode != http.StatusCreated {
		t.Fatalf("send: %d", c.StatusCode)
	}
	var sent messages.Message
	readJSON(t, c, &sent)
	if sent.Content != "Hello Bob" || sent.SenderID != "alice001" || sent.SenderUsername != "alice" {
		t.Errorf("sent: %+v", sent)
	}

	// Bob lists thread with Alice → sees the message
	l := r.do(t, "GET", "/api/messages/with/alice001", "bob00001", nil)
	var msgs []messages.Message
	readJSON(t, l, &msgs)
	if len(msgs) != 1 || msgs[0].Content != "Hello Bob" {
		t.Errorf("bob's thread: %+v", msgs)
	}

	// Bob's unread count is now 0 (listing marked-read)
	u := r.do(t, "GET", "/api/messages/unread-count", "bob00001", nil)
	var ur struct{ Count int }
	readJSON(t, u, &ur)
	if ur.Count != 0 {
		t.Errorf("bob unread after read: got %d, want 0", ur.Count)
	}
}

func TestHandler_UnreadCountReflectsNewMessages(t *testing.T) {
	r := newRig(t)
	// Alice sends Bob two messages
	r.do(t, "POST", "/api/messages/with/bob00001", "alice001",
		map[string]string{"content": "msg 1"}).Body.Close()
	r.do(t, "POST", "/api/messages/with/bob00001", "alice001",
		map[string]string{"content": "msg 2"}).Body.Close()

	u := r.do(t, "GET", "/api/messages/unread-count", "bob00001", nil)
	var ur struct{ Count int }
	readJSON(t, u, &ur)
	if ur.Count != 2 {
		t.Errorf("bob unread: got %d, want 2", ur.Count)
	}
}

func TestHandler_Conversations(t *testing.T) {
	r := newRig(t)
	r.do(t, "POST", "/api/messages/with/bob00001", "alice001",
		map[string]string{"content": "to bob"}).Body.Close()
	r.do(t, "POST", "/api/messages/with/carol001", "alice001",
		map[string]string{"content": "to carol"}).Body.Close()

	// Alice's conversations: both bob + carol
	resp := r.do(t, "GET", "/api/messages/conversations", "alice001", nil)
	var convos []messages.Conversation
	readJSON(t, resp, &convos)
	if len(convos) != 2 {
		t.Errorf("len: %d", len(convos))
	}
}

func TestHandler_PrivacyOtherUsersCantReadThread(t *testing.T) {
	r := newRig(t)
	// Alice sends Bob a message
	r.do(t, "POST", "/api/messages/with/bob00001", "alice001",
		map[string]string{"content": "secret to bob"}).Body.Close()

	// Carol queries the thread between herself and Bob — should be empty
	// (Carol hasn't messaged Bob). Not 403 — there's no information
	// disclosure: from Carol's perspective, the thread genuinely doesn't
	// exist.
	resp := r.do(t, "GET", "/api/messages/with/bob00001", "carol001", nil)
	var msgs []messages.Message
	readJSON(t, resp, &msgs)
	if len(msgs) != 0 {
		t.Errorf("carol sees alice→bob thread: %+v", msgs)
	}

	// Even more important: Carol's view of the alice↔bob thread does
	// NOT include alice's secret message (which was bob's, not carol's).
	for _, m := range msgs {
		if m.Content == "secret to bob" {
			t.Errorf("PRIVACY LEAK: carol can see alice→bob message")
		}
	}
}

func TestHandler_AuthRequired(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/api/messages/conversations"},
		{"GET", "/api/messages/unread-count"},
		{"GET", "/api/messages/with/alice001"},
		{"POST", "/api/messages/with/alice001"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := r.do(t, tc.method, tc.path, "", map[string]string{"content": "x"})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("got %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestHandler_CannotMessageSelf(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/messages/with/alice001", "alice001",
		map[string]string{"content": "hi me"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("self-message: got %d, want 400", resp.StatusCode)
	}
}

func TestHandler_RecipientNotFound(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/messages/with/ghostxxx", "alice001",
		map[string]string{"content": "hi ghost"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nonexistent recipient: got %d, want 404", resp.StatusCode)
	}
}

func TestHandler_EmptyContentRejected(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/messages/with/bob00001", "alice001",
		map[string]string{"content": "   "})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty content: got %d, want 400", resp.StatusCode)
	}
}

func TestHandler_CORSPreflight(t *testing.T) {
	r := newRig(t)
	for _, path := range []string{
		"/api/messages/conversations",
		"/api/messages/unread-count",
		"/api/messages/with/bob00001",
	} {
		t.Run(path, func(t *testing.T) {
			req, _ := http.NewRequest("OPTIONS", r.server.URL+path, nil)
			req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
			resp, _ := http.DefaultClient.Do(req)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("preflight: got %d, want 204", resp.StatusCode)
			}
		})
	}
}
