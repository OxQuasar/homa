package codeurl_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/codeurl"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// rig stands up a fresh SQLite + auth service + the codeurl handler on
// an httptest server. Pass `secret` empty to simulate feature-disabled;
// supply `csServePort=0` on the user to simulate a pre-feature row that
// hasn't been backfilled yet.
type rig struct {
	server *httptest.Server
	token  string
}

const cookieToken = "deadbeefdeadbeefdeadbeefdeadbeef"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newRig(t *testing.T, host string, secret []byte, csServePort int) *rig {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "homa.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	const userID = "abcd1234"
	u := store.User{
		ID: userID, Email: "u@x", PasswordHash: "$2a", Name: "U",
		BranchName: "user/" + userID, WorktreePath: "/wt",
		ContainerName: "homa-user-" + userID,
		NousPort:      40000, PreviewPort: 40001, PreviewServePort: 10001,
		NousSessionID:       "sess",
		CodeServerPort:      0,
		CodeServerServePort: csServePort,
		CreatedAt:           1, LastActiveAt: 1, LastMessageAt: 1, Approved: true,	}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.CreateWebSession(context.Background(), cookieToken, userID, 9_999_999_999); err != nil {
		t.Fatalf("CreateWebSession: %v", err)
	}

	authSvc := auth.New(st, nil, false, "", nil, quietLog())
	mux := http.NewServeMux()
	codeurl.NewHandler(host, secret, quietLog()).Register(mux, authSvc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &rig{server: srv, token: cookieToken}
}

// get sends GET /code-url with the auth cookie attached.
func (r *rig) get(t *testing.T) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", r.server.URL+"/code-url", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: r.token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// TestCodeURL_HappyPath — secret set + user has CodeServerServePort +
// host configured → enabled=true, URL contains expected pieces.
func TestCodeURL_HappyPath(t *testing.T) {
	r := newRig(t, "homa.example.com", []byte("a-stable-test-secret-of-32-bytes!"), 20001)
	resp, body := r.get(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var got struct {
		Enabled bool   `json:"enabled"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Enabled {
		t.Error("enabled: got false, want true")
	}
	if got.URL == "" {
		t.Fatal("URL empty")
	}
	// URL shape sanity (parsed properly + workspace as folder).
	if !strings.Contains(got.URL, "homa.example.com:20001") {
		t.Errorf("URL host:port wrong: %q", got.URL)
	}
	if !strings.Contains(got.URL, "folder=%2Fworkspace") {
		t.Errorf("URL missing folder param: %q", got.URL)
	}
}

// TestCodeURL_DisabledNoSecret — feature gate fires when no secret loaded.
func TestCodeURL_DisabledNoSecret(t *testing.T) {
	r := newRig(t, "homa.example.com", nil, 20001)
	resp, body := r.get(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got struct{ Enabled bool }
	json.Unmarshal(body, &got)
	if got.Enabled {
		t.Error("enabled: got true with empty secret, want false")
	}
}

// TestCodeURL_DisabledNoPort — pre-feature user (CodeServerServePort=0)
// gets enabled=false even when secret IS set. Until backfill allocates
// their ports, the editor hides the button.
func TestCodeURL_DisabledNoPort(t *testing.T) {
	r := newRig(t, "homa.example.com", []byte("a-stable-test-secret-of-32-bytes!"), 0)
	resp, body := r.get(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got struct{ Enabled bool }
	json.Unmarshal(body, &got)
	if got.Enabled {
		t.Error("enabled: got true with port=0, want false")
	}
}

// TestCodeURL_DisabledNoHost — host config empty (e.g. PreviewBaseURL
// not set) → feature off.
func TestCodeURL_DisabledNoHost(t *testing.T) {
	r := newRig(t, "", []byte("a-stable-test-secret-of-32-bytes!"), 20001)
	resp, body := r.get(t)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got struct{ Enabled bool }
	json.Unmarshal(body, &got)
	if got.Enabled {
		t.Error("enabled: got true with empty host, want false")
	}
}

// TestCodeURL_RequiresAuth — no cookie → 401. The endpoint is mounted
// behind authSvc.RequireAuth; this just confirms that wiring is right.
func TestCodeURL_RequiresAuth(t *testing.T) {
	r := newRig(t, "homa.example.com", []byte("a-stable-test-secret-of-32-bytes!"), 20001)
	resp, err := http.Get(r.server.URL + "/code-url")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

// TestCodeURL_DifferentUsersDifferentURLs — verifies two users get
// distinct URLs (different tkn). Important since one user knowing
// their own URL shouldn't let them derive another user's.
func TestCodeURL_DifferentUsersDifferentURLs(t *testing.T) {
	// Reuse the rig pattern but plant two users.
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "homa.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	users := []store.User{
		{ID: "alice0001", Email: "a@x", PasswordHash: "$2a", BranchName: "u/a", WorktreePath: "/wt",
			ContainerName: "homa-user-a", NousPort: 40000, PreviewPort: 40001, PreviewServePort: 10001,
			NousSessionID: "sa", CodeServerServePort: 20001, CreatedAt: 1, LastActiveAt: 1, LastMessageAt: 1, Approved: true},
		{ID: "bob00002", Email: "b@x", PasswordHash: "$2a", BranchName: "u/b", WorktreePath: "/wt",
			ContainerName: "homa-user-b", NousPort: 40002, PreviewPort: 40003, PreviewServePort: 10002,
			NousSessionID: "sb", CodeServerServePort: 20002, CreatedAt: 1, LastActiveAt: 1, LastMessageAt: 1, Approved: true},
	}
	for _, u := range users {
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.ID, err)
		}
	}
	st.CreateWebSession(context.Background(), "tok-alice", "alice0001", 9_999_999_999)
	st.CreateWebSession(context.Background(), "tok-bob000", "bob00002", 9_999_999_999)

	authSvc := auth.New(st, nil, false, "", nil, quietLog())
	mux := http.NewServeMux()
	codeurl.NewHandler("homa.example.com", []byte("a-stable-test-secret-of-32-bytes!"), quietLog()).
		Register(mux, authSvc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pull := func(token string) string {
		req, _ := http.NewRequest("GET", srv.URL+"/code-url", nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		resp, _ := http.DefaultClient.Do(req)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var got struct{ URL string }
		json.Unmarshal(b, &got)
		return got.URL
	}
	urlA := pull("tok-alice")
	urlB := pull("tok-bob000")
	if urlA == "" || urlB == "" {
		t.Fatalf("URLs empty: A=%q B=%q", urlA, urlB)
	}
	if urlA == urlB {
		t.Errorf("alice and bob got the same URL: %q", urlA)
	}
}
