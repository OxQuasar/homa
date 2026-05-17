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

// ErrBelowThreshold is returned by Run when the session's PromptTokens are
// at or below the gate threshold. The lifecycle treats this as a normal
// skip path (logs at Info, proceeds to Stop) — distinct from real errors
// like dial failures or session-busy.
var ErrBelowThreshold = errors.New("session below compaction threshold")

// Run dials ws://127.0.0.1:<nousPort>/, sends Hello, reads the initial
// session_state snapshot, gates on PromptTokens > minTokens, and (if
// past the gate) sends full_compact and waits for compact_done. Returns:
//
//   - (promptTokens, nil)               : compaction completed
//   - (promptTokens, ErrBelowThreshold) : skipped (session too small)
//   - (0, other error)                  : dial/handshake/timeout/etc
//
// promptTokens is sess.TokenUsage.TotalInputTokens() observed at the
// moment of the gate decision — the same number the editor pill shows
// (input + cache_creation + cache_read). Always returned when the snap
// was successfully read; lets the lifecycle layer log it on every path
// (skip or compact-success or compact-failure), so we always have the
// diagnostic value in the journal without paying a second round-trip.
// Returns 0 if the snap was never read.
//
// `minTokens <= 0` disables the gate (always compact).
func (c CompactClient) Run(ctx context.Context, nousPort int, sessionID, workDir string, minTokens int64, timeout time.Duration) (int64, error) {
	url := fmt.Sprintf("ws://127.0.0.1:%d/", nousPort)

	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	conn, _, err := websocket.Dial(dialCtx, url, nil)
	dialCancel()
	if err != nil {
		return 0, fmt.Errorf("dial nous: %w", err)
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
		return 0, fmt.Errorf("write hello: %w", err)
	}

	// 2. Read the initial session_state. PromptTokens here is
	// sess.TokenUsage.TotalInputTokens() — same value the editor's
	// header pill uses. Gate the compaction on this so small sessions
	// don't pay for trivial summaries.
	stateEv, err := waitForEvent(roundCtx, conn, "session_state")
	if err != nil {
		return 0, fmt.Errorf("waiting for snapshot: %w", err)
	}
	var promptTokens int64
	if stateEv.SessionState != nil {
		promptTokens = stateEv.SessionState.PromptTokens
	}
	if minTokens > 0 && promptTokens <= minTokens {
		return promptTokens, ErrBelowThreshold
	}

	// 3. Request full_compact — the more thorough variant; takes optional
	// `prompt` instructions which we leave empty (nous picks a default).
	req, _ := json.Marshal(map[string]string{"type": "full_compact"})
	if err := conn.Write(roundCtx, websocket.MessageText, req); err != nil {
		return promptTokens, fmt.Errorf("write full_compact: %w", err)
	}

	// 4. Wait for compact_done. nous's gateway emits this whether
	// compaction succeeded or it failed because the session was busy /
	// observing — in either case we move on to the Stop step.
	_, err = waitForEvent(roundCtx, conn, "compact_done")
	return promptTokens, err
}

// eventProbe is the slice of nous's Event JSON the compaction round-trip
// needs to read: the type + any err_str on terminal events + the
// session_state nested object for PromptTokens.
type eventProbe struct {
	Type         string             `json:"type"`
	ErrStr       string             `json:"err_str,omitempty"`
	SessionState *sessionStateProbe `json:"session_state,omitempty"`
}

type sessionStateProbe struct {
	PromptTokens int64 `json:"prompt_tokens"`
}

// waitForEvent reads frames until one with the matching type lands.
// Returns the parsed probe so callers can inspect typed fields (e.g.
// session_state.prompt_tokens). Non-matching frames are silently
// consumed. Returns ctx.Err() on timeout / cancellation.
func waitForEvent(ctx context.Context, conn *websocket.Conn, wantType string) (*eventProbe, error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("timed out waiting for %q event", wantType)
			}
			return nil, err
		}
		var p eventProbe
		if jerr := json.Unmarshal(data, &p); jerr != nil {
			continue // not parseable as Event — skip
		}
		if p.Type == wantType {
			if p.ErrStr != "" {
				return &p, fmt.Errorf("%s reported error: %s", wantType, p.ErrStr)
			}
			return &p, nil
		}
	}
}
