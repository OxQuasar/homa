package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/skipper/homa/orchestrator/internal/store"
)

// TestIsRunRequest — pure parser table. The peek is the hot path on every
// browser→upstream frame; this test pins the classification rules so a
// refactor can't accidentally widen or narrow what counts as a "run".
func TestIsRunRequest(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"run with prompt", `{"type":"run","prompt":"hi"}`, true},
		{"run only", `{"type":"run"}`, true},
		{"run with extra fields", `{"type":"run","prompt":"x","foo":42}`, true},
		{"different type", `{"type":"stop"}`, false},
		{"get_messages", `{"type":"get_messages"}`, false},
		{"context_stats", `{"type":"context_stats"}`, false},
		{"Hello frame (no type field)", `{"work_dir":"/workspace"}`, false},
		{"malformed JSON", `{"type":"run"`, false},
		{"empty", ``, false},
		{"not an object", `"run"`, false},
		{"type field but null", `{"type":null}`, false},
		// Casing is significant — nous emits lowercase types.
		{"upper-case Run", `{"type":"Run"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRunRequest([]byte(tc.raw)); got != tc.want {
				t.Errorf("isRunRequest(%q): got %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// peekTestEnv stands up a paired (browser, upstream) WS so we can call
// copyBrowserToUpstream against real conns. Mirrors hubTestEnv's pattern
// but pairs two server-side accepts (one "browser", one "upstream") via
// distinct channels. Simplifies test reasoning.
type peekTestEnv struct {
	server     *httptest.Server
	upstreamCh chan *websocket.Conn
	browserCh  chan *websocket.Conn
	urlBrowser string
	urlUp      string
}

func newPeekTestEnv(t *testing.T) *peekTestEnv {
	t.Helper()
	e := &peekTestEnv{
		upstreamCh: make(chan *websocket.Conn, 4),
		browserCh:  make(chan *websocket.Conn, 4),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/upstream", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(-1)
		e.upstreamCh <- c
		<-r.Context().Done()
	})
	mux.HandleFunc("/browser", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(-1)
		e.browserCh <- c
		<-r.Context().Done()
	})
	e.server = httptest.NewServer(mux)
	base := "ws" + strings.TrimPrefix(e.server.URL, "http")
	e.urlBrowser = base + "/browser"
	e.urlUp = base + "/upstream"
	t.Cleanup(e.server.Close)
	return e
}

// dialPair returns:
//   - browserClient: what the test writes "browser-side" frames into
//                    (these get peeked + forwarded by copyBrowserToUpstream)
//   - browserServer: the conn copyBrowserToUpstream READS from
//   - upstreamClient: where the forwarded frames LAND so the test can
//                     assert they made it through
//   - upstreamServer: the conn copyBrowserToUpstream WRITES to
func (e *peekTestEnv) dialPair(t *testing.T) (browserClient, browserServer, upstreamClient, upstreamServer *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var err error
	upstreamClient, _, err = websocket.Dial(ctx, e.urlUp, nil)
	if err != nil {
		t.Fatalf("dial upstream: %v", err)
	}
	upstreamClient.SetReadLimit(-1)
	upstreamServer = <-e.upstreamCh

	browserClient, _, err = websocket.Dial(ctx, e.urlBrowser, nil)
	if err != nil {
		t.Fatalf("dial browser: %v", err)
	}
	browserClient.SetReadLimit(-1)
	browserServer = <-e.browserCh

	return
}

func quietLog2() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newPeekTestStore opens a temp SQLite + seeds one user; returns the
// store and the user's id. Lets the test inspect LastMessageAt before
// and after frames flow through copyBrowserToUpstream.
func newPeekTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "homa.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	const userID = "peekuser"
	u := store.User{
		ID: userID, Email: "p@x", PasswordHash: "$2a", Name: "P",
		BranchName: "user/" + userID, WorktreePath: "/wt",
		ContainerName: "homa-user-" + userID,
		NousPort:      40000, PreviewPort: 40001, PreviewServePort: 10001,
		NousSessionID: "sess",
		CreatedAt:     1, LastActiveAt: 1, LastMessageAt: 1,
	}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return st, userID
}

// runPeekLoop is the cleanup-friendly form of running copyBrowserToUpstream
// in a goroutine; the returned cancel stops it. Used by tests that want to
// drive a few frames and then tear down.
func runPeekLoop(t *testing.T, h *handler, userID string, upstream, browser *websocket.Conn) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = copyBrowserToUpstream(ctx, upstream, browser, h, userID)
	}()
	return cancel
}

// readLastMessageAt — wait briefly for the async bumpMessage to land.
// bumpMessage runs in the same goroutine as copyBrowserToUpstream, so
// the bump is synchronous wrt frame Read; but the test still polls to
// avoid racing with SQLite's WAL fsync.
func readLastMessageAt(t *testing.T, st *store.Store, userID string) int64 {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		u, _ := st.GetUserByID(context.Background(), userID)
		if u != nil {
			return u.LastMessageAt
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("user not loadable after 500ms")
	return 0
}

// TestPeekBumpsOnRunRequest — sending a {"type":"run"} frame from the
// browser side triggers bumpMessage. The forwarded frame still arrives
// on the upstream client; the peek doesn't intercept, just observes.
func TestPeekBumpsOnRunRequest(t *testing.T) {
	env := newPeekTestEnv(t)
	st, userID := newPeekTestStore(t)
	h := &handler{store: st, hub: nil, log: quietLog2()}

	bc, bs, uc, us := env.dialPair(t)
	runPeekLoop(t, h, userID, us, bs)

	beforeBump := readLastMessageAt(t, st, userID)

	// Sleep 1s so the post-bump timestamp differs at second granularity
	// (UpdateLastMessage stores unix seconds).
	time.Sleep(1100 * time.Millisecond)

	frame, _ := json.Marshal(map[string]string{"type": "run", "prompt": "hi"})
	if err := bc.Write(context.Background(), websocket.MessageText, frame); err != nil {
		t.Fatalf("browser write: %v", err)
	}

	// Pull the forwarded frame on the upstream side so the peek loop
	// can continue (otherwise the upstream's read buffer fills).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, got, err := uc.Read(ctx); err != nil {
		t.Fatalf("upstream read: %v", err)
	} else if string(got) != string(frame) {
		t.Errorf("forwarded payload: got %q, want %q", got, frame)
	}

	afterBump := readLastMessageAt(t, st, userID)
	if afterBump <= beforeBump {
		t.Errorf("LastMessageAt: got %d, want > %d", afterBump, beforeBump)
	}

	// Cleanup: closing browser causes copyBrowserToUpstream to return.
	bc.Close(websocket.StatusNormalClosure, "test done")
}

// TestPeekIgnoresNonRunRequests — frames with other types (or no `type`)
// flow through but DON'T bump last_message_at. Verifies the peek isn't
// too permissive.
func TestPeekIgnoresNonRunRequests(t *testing.T) {
	env := newPeekTestEnv(t)
	st, userID := newPeekTestStore(t)
	h := &handler{store: st, hub: nil, log: quietLog2()}

	bc, bs, uc, us := env.dialPair(t)
	runPeekLoop(t, h, userID, us, bs)

	before := readLastMessageAt(t, st, userID)
	time.Sleep(1100 * time.Millisecond)

	nonRunFrames := []map[string]any{
		{"type": "stop"},
		{"type": "get_messages"},
		{"type": "context_stats"},
		{"work_dir": "/workspace"}, // Hello-style frame, no type field
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Drain forwarded frames as they arrive so the peek loop unblocks.
		for i := 0; i < len(nonRunFrames); i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _, err := uc.Read(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}()

	for _, payload := range nonRunFrames {
		raw, _ := json.Marshal(payload)
		if err := bc.Write(context.Background(), websocket.MessageText, raw); err != nil {
			t.Fatalf("write %v: %v", payload, err)
		}
	}
	wg.Wait()

	after := readLastMessageAt(t, st, userID)
	if after != before {
		t.Errorf("LastMessageAt changed despite no run frames: before=%d after=%d", before, after)
	}
	bc.Close(websocket.StatusNormalClosure, "test done")
}
