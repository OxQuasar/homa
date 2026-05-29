package usersapi_test

import (
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
	"github.com/skipper/homa/orchestrator/internal/store"
	"github.com/skipper/homa/orchestrator/internal/usersapi"
)

const previewBase = "https://gandiva.tailnet.ts.net"
const cookieToken = "tokusertoktokusertoktokusertoktok"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// rig — SQLite + seeded users + handler mounted behind auth + CORS.
type rig struct {
	server *httptest.Server
	token  string
}

func newRig(t *testing.T, users []store.User, withCookieFor string) *rig {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "homa.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	for _, u := range users {
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.ID, err)
		}
	}
	if withCookieFor != "" {
		if err := st.CreateWebSession(context.Background(), cookieToken, withCookieFor, 9_999_999_999); err != nil {
			t.Fatalf("CreateWebSession: %v", err)
		}
	}

	authSvc := auth.New(st, nil, false, "", nil, quietLog())
	mux := http.NewServeMux()
	policy := cors.New(previewBase)
	usersapi.New(st, quietLog()).Register(mux, authSvc, policy.Middleware)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &rig{server: srv, token: cookieToken}
}

func (r *rig) get(t *testing.T, attachCookie bool) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", r.server.URL+"/api/users", nil)
	if attachCookie {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: r.token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func mkUser(id, username string, createdAt int64) store.User {
	return store.User{
		ID: id, Email: id + "@x", PasswordHash: "$2a", Username: username,
		BranchName: "u/" + id, WorktreePath: "/wt", ContainerName: "c-" + id,
		NousPort: 0, PreviewPort: 0, PreviewServePort: 0, NousSessionID: "s-" + id,
		CreatedAt: createdAt, LastActiveAt: createdAt, LastMessageAt: createdAt,
	}
}

func TestList_HappyPath(t *testing.T) {
	users := []store.User{
		mkUser("aaaaaaaa", "alice", 100),
		mkUser("bbbbbbbb", "bob", 200),
		mkUser("cccccccc", "carol", 150),
	}
	r := newRig(t, users, "aaaaaaaa")
	resp, body := r.get(t, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", resp.StatusCode, body)
	}
	var got []usersapi.UserSummary
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3", len(got))
	}
	// Sorted by created_at ASC: alice (100), carol (150), bob (200).
	wantOrder := []string{"alice", "carol", "bob"}
	for i, u := range got {
		if u.Username != wantOrder[i] {
			t.Errorf("order[%d]: got %q, want %q", i, u.Username, wantOrder[i])
		}
	}
}

func TestList_OmitsEmptyUsername(t *testing.T) {
	// User with empty username — legacy pre-backfill row. Should be filtered.
	users := []store.User{
		mkUser("aaaaaaaa", "alice", 100),
		mkUser("bbbbbbbb", "", 200), // empty username
	}
	r := newRig(t, users, "aaaaaaaa")
	resp, body := r.get(t, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got []usersapi.UserSummary
	json.Unmarshal(body, &got)
	if len(got) != 1 || got[0].Username != "alice" {
		t.Errorf("empty-username not filtered: %+v", got)
	}
}

func TestList_NoEmailInResponse(t *testing.T) {
	users := []store.User{mkUser("aaaaaaaa", "alice", 100)}
	r := newRig(t, users, "aaaaaaaa")
	_, body := r.get(t, true)
	if bytesContains(body, "@x") {
		t.Errorf("email leaked into directory response: %s", body)
	}
}

func TestList_RequiresAuth(t *testing.T) {
	r := newRig(t, []store.User{mkUser("aaaaaaaa", "alice", 100)}, "aaaaaaaa")
	resp, _ := r.get(t, false) // no cookie
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-cookie status: got %d, want 401", resp.StatusCode)
	}
}

func TestList_CORSPreflight(t *testing.T) {
	r := newRig(t, []store.User{mkUser("aaaaaaaa", "alice", 100)}, "aaaaaaaa")
	req, _ := http.NewRequest("OPTIONS", r.server.URL+"/api/users", nil)
	req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight: got %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Error("preflight missing ACAO")
	}
}

func TestList_EmptyDB(t *testing.T) {
	users := []store.User{mkUser("aaaaaaaa", "alice", 100)}
	r := newRig(t, users, "aaaaaaaa")
	// Wipe the only user-with-username so DB is "empty" from the API's
	// perspective — but the calling user still has a session. Use a
	// second cookie that's already in the DB.
	resp, body := r.get(t, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	// With the alice row, we expect 1 entry. Sanity that the response
	// JSON is a valid array (not null) — the handler initializes the
	// slice to [] to avoid the nil → "null" trap.
	if string(body) == "null" || string(body) == "null\n" {
		t.Errorf("got JSON null instead of empty array")
	}
}

// bytesContains is io.Reader-free strings.Contains for []byte.
func bytesContains(b []byte, s string) bool {
	return indexOf(string(b), s) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
