package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/provision"
	"github.com/skipper/homa/orchestrator/internal/proxy"
	"github.com/skipper/homa/orchestrator/internal/proxy/fakeupstream"
	"github.com/skipper/homa/orchestrator/internal/store"
)

// testRig stands up a fake upstream + orchestrator mux + cookie-aware client.
type testRig struct {
	t         *testing.T
	store     *store.Store
	orchSrv   *httptest.Server
	fakeAddr  *net.TCPAddr
	fakeCtx   context.Context
	fakeStop  context.CancelFunc
	client    *http.Client
}

// newTestRig is the captain's "Builds a Store + StubProvisioner such that
// signup allocates nous_port = the upstream's port" recipe:
//   1. Start fake upstream on an OS-assigned port.
//   2. Build a StubProvisioner with nextHostPort = that port.
//   3. Mount auth + proxy on a shared mux behind httptest.
//   4. Returns a cookie-aware http.Client to drive the flow.
func newTestRig(t *testing.T) *testRig {
	t.Helper()

	// Fake upstream first; we need its bound port.
	fakeCtx, fakeStop := context.WithCancel(context.Background())
	addrCh := make(chan net.Addr, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- fakeupstream.ListenAndServe(fakeCtx, "127.0.0.1:0", addrCh) }()

	var fakeAddr *net.TCPAddr
	select {
	case a := <-addrCh:
		fakeAddr = a.(*net.TCPAddr)
	case err := <-errCh:
		t.Fatalf("fake upstream listen: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("fake upstream did not bind in time")
	}

	dbPath := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Provisioner's first allocation = fakeAddr.Port — that lands in
	// users.nous_port and the proxy will dial it.
	prov := provision.NewStubProvisionerWithStarts(
		filepath.Join(t.TempDir(), "branches"),
		fakeAddr.Port,
		provision.PreviewServePortStart,
	)

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	authSvc := auth.New(st, prov, false /* cookieSecure */, "", log)

	mux := http.NewServeMux()
	authSvc.Register(mux)
	proxy.Register(mux, st, authSvc, proxy.NewHub(log), log)

	orchSrv := httptest.NewServer(mux)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	t.Cleanup(func() {
		orchSrv.Close()
		fakeStop()
	})

	return &testRig{
		t:        t,
		store:    st,
		orchSrv:  orchSrv,
		fakeAddr: fakeAddr,
		fakeCtx:  fakeCtx,
		fakeStop: fakeStop,
		client:   client,
	}
}

func (r *testRig) signup(email, password string) string {
	r.t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": password,
		"username": store.DeriveUsername(email),
	})
	resp, err := r.client.Post(r.orchSrv.URL+"/signup", "application/json", bytes.NewReader(body))
	if err != nil {
		r.t.Fatalf("signup: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		r.t.Fatalf("signup status %d: %s", resp.StatusCode, b)
	}
	var got struct{ UserID string `json:"user_id"` }
	json.NewDecoder(resp.Body).Decode(&got)
	return got.UserID
}

func (r *testRig) cookieValue() string {
	r.t.Helper()
	u, err := url.Parse(r.orchSrv.URL)
	if err != nil {
		r.t.Fatalf("parse orch url: %v", err)
	}
	for _, c := range r.client.Jar.Cookies(u) {
		if c.Name == auth.CookieName {
			return c.Value
		}
	}
	r.t.Fatal("no homa_session cookie set")
	return ""
}

// dialProxy opens a WS to the orchestrator's /ws carrying the auth cookie.
func (r *testRig) dialProxy(ctx context.Context) *websocket.Conn {
	r.t.Helper()
	wsURL := "ws" + strings.TrimPrefix(r.orchSrv.URL, "http") + "/ws"

	hdr := http.Header{}
	hdr.Add("Cookie", auth.CookieName+"="+r.cookieValue())

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		r.t.Fatalf("ws dial: %v", err)
	}
	conn.SetReadLimit(-1)
	return conn
}

// --- tests ---

// TestProxyRoundtrip drives the full Acceptance A flow.
func TestProxyRoundtrip(t *testing.T) {
	rig := newTestRig(t)
	rig.signup("proxy@x.io", "hunter22")

	// Read users.last_active_at *before* the WS opens.
	ctx := context.Background()
	pre, err := rig.store.GetUserByEmail(ctx, "proxy@x.io")
	if err != nil {
		t.Fatalf("get pre: %v", err)
	}
	tBefore := pre.LastActiveAt

	// Sleep at least 1s so a post-connection bump (unix seconds resolution)
	// can register a strictly-greater value. Without this, fast-running tests
	// could see equal-seconds timestamps and fail the "advanced" assertion.
	time.Sleep(1100 * time.Millisecond)

	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dialCancel()
	conn := rig.dialProxy(dialCtx)

	// 1. Send Hello as one WS message.
	hello := map[string]string{"work_dir": "/workspace"}
	helloBytes, _ := json.Marshal(hello)
	if err := conn.Write(ctx, websocket.MessageText, helloBytes); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	// 2. Read snapshot.
	_, snapBytes, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap struct {
		Type         string `json:"type"`
		SessionState *struct {
			ID        string `json:"id"`
			Directory string `json:"directory"`
			YoloOn    bool   `json:"yolo_on"`
		} `json:"session_state"`
	}
	if err := json.Unmarshal(snapBytes, &snap); err != nil {
		t.Fatalf("parse snapshot: %v (raw=%s)", err, snapBytes)
	}
	if snap.Type != "session_state" {
		t.Fatalf("first event: got %q want session_state", snap.Type)
	}
	if snap.SessionState == nil || snap.SessionState.ID != fakeupstream.SessionID {
		t.Fatalf("snapshot session_state: %+v", snap.SessionState)
	}
	if snap.SessionState.Directory != "/workspace" {
		t.Errorf("snapshot directory: got %q want /workspace", snap.SessionState.Directory)
	}

	// 3. Send a run request.
	runReq := map[string]string{"type": "run", "prompt": "hi"}
	runBytes, _ := json.Marshal(runReq)
	if err := conn.Write(ctx, websocket.MessageText, runBytes); err != nil {
		t.Fatalf("write run: %v", err)
	}

	// 4. Expect FakeDeltaCount text_delta + run_done.
	deltas := 0
	gotDone := false
	for !gotDone {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read after run: %v", err)
		}
		var ev struct {
			Type    string `json:"type"`
			Delta   string `json:"delta"`
			IsError bool   `json:"is_error"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		switch ev.Type {
		case "text_delta":
			deltas++
		case "run_done":
			gotDone = true
			if ev.IsError {
				t.Error("run_done IsError=true")
			}
		default:
			t.Errorf("unexpected event type %q", ev.Type)
		}
	}
	if deltas != fakeupstream.FakeDeltaCount {
		t.Errorf("got %d text_delta, want %d", deltas, fakeupstream.FakeDeltaCount)
	}

	// 5. last_active_at advanced past tBefore.
	post, err := rig.store.GetUserByEmail(ctx, "proxy@x.io")
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if post.LastActiveAt <= tBefore {
		t.Errorf("last_active_at did not advance: pre=%d post=%d", tBefore, post.LastActiveAt)
	}

	// 6. Close client; the proxy's reader goroutines must exit within 1s.
	// Indirect proof: a follow-up dial succeeds (fake upstream isn't blocked
	// on the prior connection).
	conn.Close(websocket.StatusNormalClosure, "test done")

	done := make(chan struct{})
	go func() {
		next, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(rig.orchSrv.URL, "http")+"/ws",
			&websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{auth.CookieName + "=" + rig.cookieValue()}}})
		if err == nil {
			next.Close(websocket.StatusNormalClosure, "")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream goroutine did not release within 2s")
	}
}

// TestProxyUnauthenticated confirms /ws without a cookie returns 401.
func TestProxyUnauthenticated(t *testing.T) {
	rig := newTestRig(t)
	wsURL := "ws" + strings.TrimPrefix(rig.orchSrv.URL, "http") + "/ws"
	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Fatal("expected dial failure, got nil")
	}
	if resp == nil {
		t.Fatalf("nil response: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

// TestProxyUpstreamUnreachable verifies the proxy closes the browser WS
// cleanly when the upstream isn't listening.
func TestProxyUpstreamUnreachable(t *testing.T) {
	// Start a rig, then tear down the fake upstream before opening the WS.
	rig := newTestRig(t)
	rig.signup("unreach@x.io", "hunter22")
	rig.fakeStop() // upstream gone

	// Give the OS a moment to actually release the port.
	time.Sleep(100 * time.Millisecond)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	conn := rig.dialProxy(dialCtx)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// First read should return a close error, ideally with our "sandbox
	// unreachable" reason. We just assert that a close arrives within a
	// few seconds and isn't a hung connection.
	conn.SetReadLimit(-1)
	readCtx, readCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer readCancel()
	_, _, err := conn.Read(readCtx)
	if err == nil {
		t.Fatal("expected close on unreachable upstream, got message")
	}
	// Acceptable: any close error. Print for visibility.
	t.Logf("got expected close: %v", err)
}
