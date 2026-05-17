package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeNous stands up an HTTP-upgradeable WS endpoint that pretends to be
// the in-sandbox nous daemon. It serves a single connection at a time —
// each CompactClient.Run dial triggers a fresh accept.
//
// Behavior is scripted per-test via the Script field: an ordered list of
// frames the server emits in response to client writes. Lets tests cover
// the happy path, the gate, error events, timeout, etc. without needing
// a real nous behind it.
type fakeNous struct {
	server *httptest.Server
	port   int

	mu       sync.Mutex
	gotHello string // last Hello payload (raw)
	gotReq   string // last request payload after Hello (raw)
	// Script returns the frames to emit AFTER each client message. The
	// server emits Script[0] immediately on connect (the initial
	// snapshot), then alternates per-message.
	Script [][]byte
	// PreSendDelay delays the FIRST emit (snapshot) — used to test the
	// dial → handshake-timeout edge.
	PreSendDelay time.Duration
}

func newFakeNous(t *testing.T, script [][]byte) *fakeNous {
	t.Helper()
	f := &fakeNous{Script: script}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Logf("fakeNous accept: %v", err)
			return
		}
		c.SetReadLimit(-1)
		defer c.Close(websocket.StatusNormalClosure, "")

		// 1. Read Hello.
		_, hello, err := c.Read(r.Context())
		if err != nil {
			return
		}
		f.mu.Lock()
		f.gotHello = string(hello)
		script := f.Script
		f.mu.Unlock()

		// 2. Emit Script[0] (snapshot). Honor PreSendDelay if any.
		if f.PreSendDelay > 0 {
			select {
			case <-time.After(f.PreSendDelay):
			case <-r.Context().Done():
				return
			}
		}
		if len(script) >= 1 {
			_ = c.Write(r.Context(), websocket.MessageText, script[0])
		}

		// 3. Optionally read the next client message (full_compact) and
		//    emit Script[1] (compact_done). If the gate skipped, client
		//    closes here without sending — that's fine.
		if len(script) >= 2 {
			c.SetReadLimit(-1)
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			_, req, err := c.Read(ctx)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.gotReq = string(req)
			f.mu.Unlock()
			_ = c.Write(r.Context(), websocket.MessageText, script[1])
		}
	}))
	// Extract numeric port for CompactClient.Run.
	hostPort := strings.TrimPrefix(f.server.URL, "http://")
	_, p, _ := net.SplitHostPort(hostPort)
	f.port, _ = strconv.Atoi(p)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeNous) lastHello() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotHello
}
func (f *fakeNous) lastRequest() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotReq
}

// helpers for building scripted events
func sessionStateFrame(promptTokens int64) []byte {
	b, _ := json.Marshal(map[string]any{
		"type": "session_state",
		"session_state": map[string]any{
			"id":            "sess-x",
			"prompt_tokens": promptTokens,
		},
	})
	return b
}
func compactDoneFrame(errStr string) []byte {
	m := map[string]any{"type": "compact_done"}
	if errStr != "" {
		m["err_str"] = errStr
	}
	b, _ := json.Marshal(m)
	return b
}

// TestCompactClientHappyPath — session past threshold, server emits
// session_state(50001) then compact_done(""), client returns nil and the
// fake nous saw exactly the right Hello + full_compact request payloads.
func TestCompactClientHappyPath(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(50_001),
		compactDoneFrame(""),
	})
	c := CompactClient{}
	_, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 3*time.Second)
	if err != nil {
		t.Fatalf("Run: got %v, want nil", err)
	}
	// Hello carried the pinned session id + workdir.
	if !strings.Contains(fn.lastHello(), `"session_id":"sess-x"`) {
		t.Errorf("Hello missing session_id: %s", fn.lastHello())
	}
	if !strings.Contains(fn.lastHello(), `"work_dir":"/workspace"`) {
		t.Errorf("Hello missing work_dir: %s", fn.lastHello())
	}
	// And full_compact was actually sent (the gate let us through).
	if !strings.Contains(fn.lastRequest(), `"full_compact"`) {
		t.Errorf("upstream got %q, want full_compact request", fn.lastRequest())
	}
}

// TestCompactClientBelowThresholdGate — session below minTokens; client
// returns ErrBelowThreshold and DOES NOT send full_compact. Also
// asserts that the prompt_tokens value is surfaced in the return — the
// lifecycle layer needs it for the skipped-log line so operators can
// see what the gate actually saw.
func TestCompactClientBelowThresholdGate(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(2_000), // well below 50k gate
		compactDoneFrame(""),     // never reached
	})
	c := CompactClient{}
	promptTokens, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 3*time.Second)
	if !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("Run: got %v, want ErrBelowThreshold", err)
	}
	if promptTokens != 2_000 {
		t.Errorf("promptTokens returned: got %d, want 2000 (matches snap value)", promptTokens)
	}
	if fn.lastRequest() != "" {
		t.Errorf("client sent a request despite below-threshold: %q", fn.lastRequest())
	}
}

// TestCompactClientReturnsPromptTokensOnSuccess — even when the gate
// passes and compaction runs, the snap's prompt_tokens is returned to
// the caller. Lifecycle uses it in the 'gc compaction complete' log
// line so operators see why each compaction was allowed through.
func TestCompactClientReturnsPromptTokensOnSuccess(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(123_456),
		compactDoneFrame(""),
	})
	c := CompactClient{}
	promptTokens, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 3*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if promptTokens != 123_456 {
		t.Errorf("promptTokens: got %d, want 123456", promptTokens)
	}
}

// TestCompactClientGateDisabled — minTokens=0 means "always compact";
// even a tiny session sends the request and gets compact_done.
func TestCompactClientGateDisabled(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(123),
		compactDoneFrame(""),
	})
	c := CompactClient{}
	_, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 0, /*timeout*/ 3*time.Second)
	if err != nil {
		t.Fatalf("Run with gate disabled: got %v, want nil", err)
	}
	if !strings.Contains(fn.lastRequest(), `"full_compact"`) {
		t.Errorf("gate=0 path didn't send full_compact: %q", fn.lastRequest())
	}
}

// TestCompactClientGateBoundary — PromptTokens equal to minTokens is on
// the SKIP side (`<=` semantics in compact.go), confirming the boundary.
func TestCompactClientGateBoundary(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(50_000), // exactly at gate
		compactDoneFrame(""),
	})
	c := CompactClient{}
	_, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 3*time.Second)
	if !errors.Is(err, ErrBelowThreshold) {
		t.Errorf("gate boundary (==): got %v, want ErrBelowThreshold", err)
	}
}

// TestCompactClientCompactDoneWithError — server emits compact_done with
// err_str (e.g. "session busy"); client returns an error (not nil, not
// ErrBelowThreshold). Lifecycle still proceeds to Stop, but the error
// carries diagnostic info.
func TestCompactClientCompactDoneWithError(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(100_000),
		compactDoneFrame("session busy"),
	})
	c := CompactClient{}
	_, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 3*time.Second)
	if err == nil {
		t.Fatal("Run: got nil, want non-nil error from compact_done.err_str")
	}
	if errors.Is(err, ErrBelowThreshold) {
		t.Errorf("Run: got ErrBelowThreshold, want generic error")
	}
	if !strings.Contains(err.Error(), "session busy") {
		t.Errorf("error doesn't surface err_str: %v", err)
	}
}

// TestCompactClientTimeout — server delays the snapshot past the per-Run
// timeout; client returns a timeout error.
func TestCompactClientTimeout(t *testing.T) {
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(100_000),
		compactDoneFrame(""),
	})
	fn.PreSendDelay = 500 * time.Millisecond
	c := CompactClient{}
	// Timeout shorter than the server's delay.
	_, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 100*time.Millisecond)
	if err == nil {
		t.Fatal("Run: got nil, want timeout error")
	}
}

// TestCompactClientDialFailure — port nothing is listening on; client
// returns a non-nil error referencing the dial step.
func TestCompactClientDialFailure(t *testing.T) {
	// Reserve a port and immediately release it.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	c := CompactClient{}
	_, err := c.Run(context.Background(), port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 1*time.Second)
	if err == nil {
		t.Fatal("Run against closed port: got nil, want dial error")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("expected dial-flavored error, got %v", err)
	}
}

// TestCompactClientHelloHasNoCompactFieldWhenGated — sanity-double-check
// that when the gate fires, the connection closes cleanly without
// attempting to write the compact request. (Implicit in
// TestCompactClientBelowThresholdGate but spelled out here so the
// invariant is named.)
func TestCompactClientHelloOnlyWhenGated(t *testing.T) {
	// Just ONE script entry — the snapshot. No second frame, because
	// the server won't be asked to emit one.
	fn := newFakeNous(t, [][]byte{
		sessionStateFrame(1000),
	})
	c := CompactClient{}
	_, err := c.Run(context.Background(), fn.port, "sess-x", "/workspace",
		/*minTokens*/ 50_000, /*timeout*/ 3*time.Second)
	if !errors.Is(err, ErrBelowThreshold) {
		t.Errorf("got %v, want ErrBelowThreshold", err)
	}
}
