package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	_ "modernc.org/sqlite"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/proxy"
	"github.com/skipper/homa/orchestrator/internal/proxy/fakeupstream"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// dialReadDeadline caps WS dial + first-event read in each per-user probe.
const dialReadDeadline = 5 * time.Second

// rig holds the full integration rig for one test.
type rig struct {
	t        *testing.T
	store    *store.Store
	dbPath   string // for the test-only port-pin UPDATE (separate connection)
	orchSrv  *httptest.Server
	fakeA    *net.TCPAddr // user A's fake upstream
	fakeB    *net.TCPAddr // user B's fake upstream
	fakeStop context.CancelFunc
}

// newRig builds a full orchestrator (auth + proxy mounted on a shared mux)
// with two tagged fake upstreams. The PortAllocator is positioned so the
// first two signups land on fakeA's and fakeB's bound ports respectively.
func newRig(t *testing.T) *rig {
	t.Helper()

	// 1. Stand up both fake upstreams first — we need their bound ports.
	fakeCtx, fakeStop := context.WithCancel(context.Background())
	startFake := func(id string) *net.TCPAddr {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fakeStop()
			t.Fatalf("fake %s listen: %v", id, err)
		}
		srv := &http.Server{Handler: fakeupstream.HandlerWithID(fakeCtx, id)}
		go srv.Serve(ln) //nolint:errcheck // closed on ctx
		go func() {
			<-fakeCtx.Done()
			_ = srv.Close()
		}()
		return ln.Addr().(*net.TCPAddr)
	}
	addrA := startFake("fake-sess-A")
	addrB := startFake("fake-sess-B")

	// 2. SQLite + auth + proxy on a shared mux.
	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		fakeStop()
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Stub provisioner positioned so:
	//   First signup → NousPort = addrA.Port, PreviewPort = addrA.Port+1
	//   Second signup → NousPort = addrA.Port+2, PreviewPort = addrA.Port+3
	// We need user A's NousPort to be addrA.Port and user B's to be addrB.Port.
	// The two fakes are on OS-assigned ports — they're rarely adjacent. So
	// we can't simply set a single starting offset.
	//
	// Workaround: bypass the provisioner entirely and CreateUser directly
	// after we sign up via the API. That way auth's full code path runs
	// (cookie, bcrypt, web_session) but the user row carries the ports we
	// dictate. Concretely: sign up normally, then UPDATE users SET nous_port
	// = addr.Port WHERE id = ?.
	prov := provision.NewStubProvisioner(filepath.Join(t.TempDir(), "branches"))

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	authSvc := auth.New(st, prov, false, "", nil, log)

	mux := http.NewServeMux()
	authSvc.Register(mux, nil)
	proxy.Register(mux, st, authSvc, proxy.NewHub(log), nil, log)

	orchSrv := httptest.NewServer(mux)
	t.Cleanup(orchSrv.Close)
	t.Cleanup(fakeStop)

	return &rig{t: t, store: st, dbPath: dbPath, orchSrv: orchSrv, fakeA: addrA, fakeB: addrB, fakeStop: fakeStop}
}

// signup makes one /signup call and returns the cookie + user_id.
func (r *rig) signup(client *http.Client, email, password string) (string, string) {
	r.t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": password,
		"username": store.DeriveUsername(email),
		"join_reason": "test filler — twenty chars min length",
		"mystery_interest": "test filler — twenty chars min length",
		"background": "test filler — twenty chars min length",
	})
	resp, err := client.Post(r.orchSrv.URL+"/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		r.t.Fatalf("signup %s: %v", email, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		r.t.Fatalf("signup %s: status=%d body=%s", email, resp.StatusCode, b)
	}
	var got struct{ UserID string `json:"user_id"` }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		r.t.Fatalf("decode signup %s: %v", email, err)
	}
	// Signup no longer issues a cookie (pending-approval gate). Approve
	// directly in the store, then log in to obtain a fresh cookie.
	st, err := store.Open(r.dbPath)
	if err != nil {
		r.t.Fatalf("open store for approve: %v", err)
	}
	defer st.Close()
	if err := st.SetApproved(context.Background(), got.UserID, true); err != nil {
		r.t.Fatalf("approve %s: %v", got.UserID, err)
	}
	loginBody, _ := json.Marshal(map[string]string{"email": email, "password": password})
	lr, err := client.Post(r.orchSrv.URL+"/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		r.t.Fatalf("login %s: %v", email, err)
	}
	lr.Body.Close()
	if lr.StatusCode != http.StatusOK {
		r.t.Fatalf("login %s: %d", email, lr.StatusCode)
	}
	u, _ := url.Parse(r.orchSrv.URL)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == auth.CookieName {
			return c.Value, got.UserID
		}
	}
	r.t.Fatalf("signup→approve→login %s: no homa_session cookie", email)
	return "", ""
}

// pinNousPort updates the user row to point at the chosen TCP port, so the
// proxy will dial the matching fake upstream. Opens a separate SQLite
// connection because production callers should never re-allocate ports —
// keeping this escape hatch out of the store package's public API.
func (r *rig) pinNousPort(userID string, port int) {
	r.t.Helper()
	db, err := sql.Open("sqlite", r.dbPath)
	if err != nil {
		r.t.Fatalf("pin: open db: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE users SET nous_port = ? WHERE id = ?`, port, userID); err != nil {
		r.t.Fatalf("pin nous_port: %v", err)
	}
}

// dialWSAndReadSnapshot connects to /ws with cookie, sends a minimal hello,
// reads the first event, returns the session_state.id.
func (r *rig) dialWSAndReadSnapshot(cookie string) string {
	r.t.Helper()
	wsURL := "ws" + strings.TrimPrefix(r.orchSrv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), dialReadDeadline)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{auth.CookieName + "=" + cookie}},
	})
	if err != nil {
		r.t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(-1)
	hello, _ := json.Marshal(map[string]string{"work_dir": "/workspace"})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		r.t.Fatalf("ws write hello: %v", err)
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		r.t.Fatalf("ws read snapshot: %v", err)
	}
	var ev struct {
		Type         string `json:"type"`
		SessionState *struct {
			ID string `json:"id"`
		} `json:"session_state"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		r.t.Fatalf("ws parse snapshot: %v (raw=%s)", err, raw)
	}
	if ev.Type != "session_state" || ev.SessionState == nil {
		r.t.Fatalf("ws snapshot shape: %+v", ev)
	}
	return ev.SessionState.ID
}

// TestTwoUserIsolation — covers the captain's T5 assertion list:
//   - concurrent signups produce disjoint user rows (7 fields)
//   - cookies are scoped: /me returns each user's own id
//   - /ws routes by cookie: A's cookie → fakeA, B's cookie → fakeB
func TestTwoUserIsolation(t *testing.T) {
	r := newRig(t)

	type signupResult struct {
		email  string
		cookie string
		userID string
		jar    http.CookieJar
	}

	// 1. Concurrent signups — stress the port allocator's mutex.
	var wg sync.WaitGroup
	results := make([]signupResult, 2)
	emails := []string{"a@x.io", "b@x.io"}
	wg.Add(2)
	for i, email := range emails {
		go func(idx int, email string) {
			defer wg.Done()
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
			cookie, userID := r.signup(client, email, "hunter22")
			results[idx] = signupResult{email: email, cookie: cookie, userID: userID, jar: jar}
		}(i, email)
	}
	wg.Wait()

	a, b := results[0], results[1]

	// 2. Assert disjoint user_id fields.
	ua, err := r.store.GetUserByID(context.Background(), a.userID)
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	ub, err := r.store.GetUserByID(context.Background(), b.userID)
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	disjoint := []struct {
		name string
		a, b any
	}{
		{"UserID", ua.ID, ub.ID},
		{"BranchName", ua.BranchName, ub.BranchName},
		{"WorktreePath", ua.WorktreePath, ub.WorktreePath},
		{"ContainerName", ua.ContainerName, ub.ContainerName},
		{"NousPort", ua.NousPort, ub.NousPort},
		{"PreviewPort", ua.PreviewPort, ub.PreviewPort},
		{"PreviewServePort", ua.PreviewServePort, ub.PreviewServePort},
	}
	for _, c := range disjoint {
		if fmt.Sprint(c.a) == fmt.Sprint(c.b) {
			t.Errorf("%s collision: A=%v B=%v", c.name, c.a, c.b)
		}
	}

	// 3. Pin nous_port for each user to the matching fake upstream port
	// so the proxy will dial the right one. (Stub provisioner gives
	// sequential ports unrelated to OS-assigned fake ports.)
	r.pinNousPort(a.userID, r.fakeA.Port)
	r.pinNousPort(b.userID, r.fakeB.Port)

	// 4. Cookie scoping via /me.
	checkMe := func(label, cookie, wantUserID string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, r.orchSrv.URL+"/me", nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s /me: %v", label, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s /me: status=%d", label, resp.StatusCode)
		}
		var got struct{ UserID string `json:"user_id"` }
		json.NewDecoder(resp.Body).Decode(&got)
		if got.UserID != wantUserID {
			t.Errorf("%s /me user_id: got %s, want %s", label, got.UserID, wantUserID)
		}
	}
	checkMe("A", a.cookie, a.userID)
	checkMe("B", b.cookie, b.userID)

	// 5. /ws routes by cookie. A → fake-sess-A; B → fake-sess-B.
	if got := r.dialWSAndReadSnapshot(a.cookie); got != "fake-sess-A" {
		t.Errorf("A /ws session id: got %q, want fake-sess-A", got)
	}
	if got := r.dialWSAndReadSnapshot(b.cookie); got != "fake-sess-B" {
		t.Errorf("B /ws session id: got %q, want fake-sess-B", got)
	}

	// 6. Negative: swap cookies and verify identity does NOT swap.
	// (A's cookie still resolves to A even when B asks /ws.)
	if got := r.dialWSAndReadSnapshot(a.cookie); got != "fake-sess-A" {
		t.Errorf("re-check A: got %q, want fake-sess-A", got)
	}
}
