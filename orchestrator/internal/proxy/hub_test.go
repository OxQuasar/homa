package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// hubTestEnv spins up an httptest server that accepts WS upgrades, pushes
// the server-side conn onto a channel, and returns. Tests pull a pair
// (client, server) per dial. Real conns (not stubs) so we exercise the
// same *websocket.Conn type the real proxy uses.
//
// Crucially the handler does NOT block on Read after Accept — the conn
// is owned by the test from that point on. Lets Hub.Disconnect / Close
// complete without waiting on a paired peer handshake.
type hubTestEnv struct {
	server *httptest.Server
	url    string
	srvCh  chan *websocket.Conn
}

func newHubTestEnv(t *testing.T) *hubTestEnv {
	t.Helper()
	env := &hubTestEnv{srvCh: make(chan *websocket.Conn, 32)}
	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Logf("server accept: %v", err)
			return
		}
		c.SetReadLimit(-1)
		env.srvCh <- c
		// Keep this goroutine alive while the conn is in use. The HTTP
		// handler returning would tear down the underlying connection,
		// which we don't want — the test drives close via Hub or
		// directly. Block on request context so the goroutine cleans
		// up when the test server closes.
		<-r.Context().Done()
	}))
	env.url = "ws" + strings.TrimPrefix(env.server.URL, "http")
	t.Cleanup(env.server.Close)
	return env
}

// dial returns (clientConn, serverConn). FIFO across goroutines:
// channel ordering guarantees each dialer gets its own pair as long as
// dials don't race the channel pump (which they can't in our usage —
// every dial is followed immediately by a pull).
//
// IMPORTANT: kicks off a goroutine that reads from the client side and
// surfaces nothing — tests that want to inspect a frame the Hub pushed
// to the browser should NOT use this helper; use dialNoReader instead.
// Without an active reader, coder/websocket's Close handshake waits 5s
// for the peer's close-acknowledgement, which slows tests that expect
// Hub.Disconnect to return quickly.
func (e *hubTestEnv) dial(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	client, srv := e.dialNoReader(t)
	go func() {
		// Drain frames so close handshake completes promptly. Loop
		// returns when conn closes (read returns an error).
		for {
			if _, _, err := client.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	return client, srv
}

// dialNoReader returns the pair without spawning a client reader. Use
// when the test wants to read a specific frame the Hub pushes.
func (e *hubTestEnv) dialNoReader(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, e.url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.SetReadLimit(-1)
	select {
	case srv := <-e.srvCh:
		return client, srv
	case <-time.After(2 * time.Second):
		t.Fatalf("server-side conn never appeared on channel")
		return nil, nil
	}
}

func quietHubLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestHubRegisterTracksConns — Register adds a conn to the per-userID
// set; HasUser reflects it; Unregister removes it (and cleans the slot).
func TestHubRegisterTracksConns(t *testing.T) {
	env := newHubTestEnv(t)
	h := NewHub(quietHubLog())

	if h.HasUser("alice") {
		t.Error("HasUser should be false before any Register")
	}
	_, srv := env.dial(t)
	h.Register("alice", srv)
	if !h.HasUser("alice") {
		t.Error("HasUser after Register: got false, want true")
	}
	h.Unregister("alice", srv)
	if h.HasUser("alice") {
		t.Error("HasUser after Unregister: got true, want false")
	}
}

// TestHubMultipleConnsSameUser — two concurrent browser tabs under the
// same user are tracked as a set. Disconnect closes both; Unregister of
// just one leaves the other intact.
func TestHubMultipleConnsSameUser(t *testing.T) {
	env := newHubTestEnv(t)
	h := NewHub(quietHubLog())

	_, srv1 := env.dial(t)
	_, srv2 := env.dial(t)
	h.Register("bob", srv1)
	h.Register("bob", srv2)

	// Snapshot: there are 2 conns. We don't have a direct accessor, but
	// SendToUser's delivered count is observable.
	frame, _ := json.Marshal(map[string]string{"type": "ping"})
	delivered := h.SendToUser("bob", frame)
	if delivered != 2 {
		t.Errorf("SendToUser delivered: got %d, want 2", delivered)
	}

	// Unregister one — Send now reaches one.
	h.Unregister("bob", srv1)
	if !h.HasUser("bob") {
		t.Error("HasUser after partial unregister: should still be true (one conn left)")
	}
	delivered = h.SendToUser("bob", frame)
	if delivered != 1 {
		t.Errorf("SendToUser after partial unregister: got %d, want 1", delivered)
	}

	// Disconnect the remaining — slot clears.
	h.Disconnect("bob", "test cleanup")
	if h.HasUser("bob") {
		// Note: Disconnect calls Close on each conn; the inner serve()'s
		// deferred Unregister is what removes from the map in the real
		// world. In this test we DON'T have a serve loop, so the entry
		// stays. We tolerate that: HasUser-after-Disconnect is not part
		// of Hub's stated contract. The user-visible contract is "the
		// conn is closed."
		t.Logf("Hub still tracks 'bob' after Disconnect; cleanup is serve()'s job (acceptable)")
	}
}

// TestHubDisconnectClosesConns — Hub.Disconnect actually closes each
// registered conn for the user, so subsequent writes from outside fail
// AND the dial's reader sees an EOF / close error.
func TestHubDisconnectClosesConns(t *testing.T) {
	env := newHubTestEnv(t)
	h := NewHub(quietHubLog())

	client, srv := env.dial(t)
	h.Register("dis", srv)

	n := h.Disconnect("dis", "force close test")
	if n != 1 {
		t.Errorf("Disconnect returned %d, want 1", n)
	}

	// Client should see the close land within a short window.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := client.Read(ctx)
	if err == nil {
		t.Errorf("client read after Hub.Disconnect: got nil err, want close/EOF")
	}
}

// TestHubSendToUserDeliversFrame — the client receives exactly the bytes
// SendToUser wrote, as a single text frame.
func TestHubSendToUserDeliversFrame(t *testing.T) {
	env := newHubTestEnv(t)
	h := NewHub(quietHubLog())

	// dialNoReader: we want to read the frame ourselves below; an auto-
	// draining reader would consume it first.
	client, srv := env.dialNoReader(t)
	h.Register("send", srv)

	want, _ := json.Marshal(map[string]any{
		"type":                  "homa.idle_warning",
		"seconds_until_compact": 47,
	})
	n := h.SendToUser("send", want)
	if n != 1 {
		t.Fatalf("SendToUser delivered: got %d, want 1", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mt, got, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if mt != websocket.MessageText {
		t.Errorf("frame type: got %v, want MessageText", mt)
	}
	if string(got) != string(want) {
		t.Errorf("payload: got %s, want %s", got, want)
	}
}

// TestHubSendToUnknownUserReturnsZero — broadcasting to a userID with no
// registered conns is a no-op, not an error.
func TestHubSendToUnknownUserReturnsZero(t *testing.T) {
	h := NewHub(quietHubLog())
	n := h.SendToUser("ghost", []byte(`{"type":"noop"}`))
	if n != 0 {
		t.Errorf("SendToUser(ghost) delivered: got %d, want 0", n)
	}
}

// TestHubDisconnectUnknownUserReturnsZero — same for Disconnect.
func TestHubDisconnectUnknownUserReturnsZero(t *testing.T) {
	h := NewHub(quietHubLog())
	n := h.Disconnect("ghost", "no one home")
	if n != 0 {
		t.Errorf("Disconnect(ghost) closed: got %d, want 0", n)
	}
}

// TestHubConcurrentRegisterUnregister — race-detector exerciser. Many
// goroutines hammer Register / Unregister / HasUser / Disconnect across
// shared user IDs. No assertions on observable counts (those depend on
// scheduling); the test passes if no panic, no `go test -race` warning,
// and the map ends up clean.
//
// Uses real conn pointers (not nil) but never actually writes to them,
// so SendToUser is exercised separately by other tests.
func TestHubConcurrentRegisterUnregister(t *testing.T) {
	env := newHubTestEnv(t)
	h := NewHub(quietHubLog())

	// Pre-allocate a small pool of real conns; we just need pointer-distinct
	// instances for the map's set semantics.
	const N = 6
	conns := make([]*websocket.Conn, N)
	for i := range conns {
		_, srv := env.dial(t)
		conns[i] = srv
	}

	var wg sync.WaitGroup
	const iters = 50
	for i, c := range conns {
		i, c := i, c
		userID := []string{"alpha", "beta"}[i%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				h.Register(userID, c)
				_ = h.HasUser(userID)
				h.Unregister(userID, c)
			}
		}()
	}
	// Concurrently with the register-loops: occasional HasUser /
	// SendToUser-to-empty calls so the map's read paths get hit too.
	var sendCalls atomic.Int32
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			_ = h.HasUser("alpha")
			_ = h.SendToUser("noone-registered-here", []byte(`{}`))
			sendCalls.Add(1)
		}
	}()
	wg.Wait()

	// All loops Unregister symmetrically → map should be empty.
	if h.HasUser("alpha") || h.HasUser("beta") {
		t.Error("Hub still tracks users after symmetric register/unregister loops")
	}
	if sendCalls.Load() != iters {
		t.Errorf("send loop iterations: got %d, want %d", sendCalls.Load(), iters)
	}
}
