// Package fakeupstream is a tiny WS server that speaks just enough of the
// nous wire protocol to exercise the orchestrator's /ws proxy without a real
// sandbox.
//
// Per browser session it:
//   1. Accepts the WS upgrade.
//   2. Reads one Hello{work_dir} message.
//   3. Sends one session_state event with a fixed id + the received WorkDir.
//   4. For each incoming Request, emits FakeDeltaCount text_delta events
//      followed by one run_done event (no error).
//
// Wire JSON shapes are hand-authored (no nous import) so the helper stays
// dependency-light. cmd/main.go is a thin standalone binary wrapper.
package fakeupstream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/coder/websocket"
)

// FakeDeltaCount is the number of EventTextDelta messages emitted per run.
// Public so tests can assert the exact same value.
const FakeDeltaCount = 2

// SessionID is the session id baked into every emitted session_state.
const SessionID = "fake-sess"

type hello struct {
	WorkDir string `json:"work_dir"`
}

type snapshot struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	YoloOn    bool   `json:"yolo_on"`
}

type event struct {
	Type         string    `json:"type"`
	SessionState *snapshot `json:"session_state,omitempty"`
	Delta        string    `json:"delta,omitempty"`
	IsError      bool      `json:"is_error,omitempty"`
}

type request struct {
	Type   string `json:"type"`
	Prompt string `json:"prompt"`
}

// Handler returns an http.Handler that drives the fake protocol with the
// default SessionID. Equivalent to HandlerWithID(ctx, SessionID).
func Handler(ctx context.Context) http.Handler {
	return HandlerWithID(ctx, SessionID)
}

// HandlerWithID is Handler with a custom session id in the emitted
// session_state. Used by isolation tests that need two upstreams emitting
// distinct identifiers so the proxy can be observed routing correctly.
func HandlerWithID(ctx context.Context, sessionID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			log.Printf("fakeupstream: accept: %v", err)
			return
		}
		conn.SetReadLimit(-1)
		defer conn.Close(websocket.StatusNormalClosure, "")
		if err := serveOnce(ctx, conn, sessionID); err != nil {
			log.Printf("fakeupstream: session: %v", err)
		}
	})
}

// ListenAndServe starts the fake on addr. If addr ends in ":0" the OS picks
// a free port; the bound *net.TCPAddr is returned (after the listener is
// open) via the addrCh callback. Blocks until ctx is cancelled or the server
// errors.
func ListenAndServe(ctx context.Context, addr string, addrCh chan<- net.Addr) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if addrCh != nil {
		addrCh <- ln.Addr()
	}
	srv := &http.Server{Handler: Handler(ctx)}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func serveOnce(ctx context.Context, conn *websocket.Conn, sessionID string) error {
	_, helloBytes, err := conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	var h hello
	if err := json.Unmarshal(helloBytes, &h); err != nil {
		return fmt.Errorf("parse hello: %w", err)
	}

	snap := event{Type: "session_state", SessionState: &snapshot{
		ID:        sessionID,
		Directory: h.WorkDir,
		YoloOn:    true,
	}}
	if err := writeJSON(ctx, conn, snap); err != nil {
		return err
	}

	for {
		_, reqBytes, err := conn.Read(ctx)
		if err != nil {
			return nil // client disconnected — normal termination
		}
		var req request
		if err := json.Unmarshal(reqBytes, &req); err != nil {
			return fmt.Errorf("parse request: %w", err)
		}
		for i := 0; i < FakeDeltaCount; i++ {
			delta := fmt.Sprintf("delta-%d-for-%s", i, req.Prompt)
			if err := writeJSON(ctx, conn, event{Type: "text_delta", Delta: delta}); err != nil {
				return err
			}
		}
		if err := writeJSON(ctx, conn, event{Type: "run_done"}); err != nil {
			return err
		}
	}
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
