// compact.go — open a WS client to the user's sandbox nous, run a
// full-compact, wait for completion. Used by the idle-compact lifecycle
// step before stopping the container.
//
// Wire protocol mirrors the editor's: Hello{WorkDir, SessionID} →
// EventSessionState snapshot → send Request{Type:"full_compact"} →
// wait for Event{Type:"compact_done"}.
//
// Note: nous's gateway locks the session to one connection at a time.
// Caller is responsible for ensuring no browser WS holds the lock
// (lifecycle calls hub.Disconnect first).

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

// CompactClient runs a single full-compact round-trip against a user's
// sandbox nous. Stateless; one method, no construction.
type CompactClient struct{}

// Run dials ws://127.0.0.1:<nousPort>/, sends Hello + full_compact, waits
// for compact_done (or timeout), and closes. Returns nil on success;
// errors on dial/handshake/wait/timeout. Failures here are non-fatal at
// the lifecycle layer — the caller logs + proceeds to Stop anyway.
func (c CompactClient) Run(ctx context.Context, nousPort int, sessionID, workDir string, timeout time.Duration) error {
	url := fmt.Sprintf("ws://127.0.0.1:%d/", nousPort)

	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	conn, _, err := websocket.Dial(dialCtx, url, nil)
	dialCancel()
	if err != nil {
		return fmt.Errorf("dial nous: %w", err)
	}
	conn.SetReadLimit(-1)
	defer conn.Close(websocket.StatusNormalClosure, "compact done")

	roundCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 1. Hello — pin the session id; nous reuses the existing session.
	hello, _ := json.Marshal(map[string]string{
		"work_dir":   workDir,
		"session_id": sessionID,
	})
	if err := conn.Write(roundCtx, websocket.MessageText, hello); err != nil {
		return fmt.Errorf("write hello: %w", err)
	}

	// 2. Drain events until we see session_state (snapshot landed).
	if err := waitForEvent(roundCtx, conn, "session_state"); err != nil {
		return fmt.Errorf("waiting for snapshot: %w", err)
	}

	// 3. Request full_compact — the more thorough variant; takes optional
	// `prompt` instructions which we leave empty (nous picks a default).
	req, _ := json.Marshal(map[string]string{"type": "full_compact"})
	if err := conn.Write(roundCtx, websocket.MessageText, req); err != nil {
		return fmt.Errorf("write full_compact: %w", err)
	}

	// 4. Wait for compact_done. nous's gateway emits this whether
	// compaction succeeded or it failed because the session was busy /
	// observing — in either case we move on to the Stop step.
	return waitForEvent(roundCtx, conn, "compact_done")
}

// waitForEvent reads frames until one with the matching type lands.
// Other events (session_state, text_delta, etc.) are silently consumed.
// Returns ctx.Err() on timeout / cancellation.
func waitForEvent(ctx context.Context, conn *websocket.Conn, wantType string) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for %q event", wantType)
			}
			return err
		}
		var probe struct {
			Type string `json:"type"`
			// compact_done carries an optional err_str when something
			// failed (e.g. "session busy"); surface it for diagnostics.
			ErrStr string `json:"err_str,omitempty"`
		}
		if jerr := json.Unmarshal(data, &probe); jerr != nil {
			continue // not parseable as Event — skip
		}
		if probe.Type == wantType {
			if probe.ErrStr != "" {
				return fmt.Errorf("%s reported error: %s", wantType, probe.ErrStr)
			}
			return nil
		}
	}
}
