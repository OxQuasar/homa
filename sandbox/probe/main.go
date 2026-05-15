// probe is a tiny WS health check for the homa sandbox.
//
//   1. Dial --addr (e.g. ws://localhost:49000/).
//   2. Send a Hello{WorkDir: --workdir} message as one WS text frame.
//   3. Read the next WS message; assert it parses as an Event of type
//      "session_state" with a non-empty session_id.
//   4. Print {session_id, directory, yolo_on} to stdout as JSON, exit 0.
//
// Wire framing matches nous internal/transport: one WS message == one
// newline-delimited JSON line, with the newline stripped before sending
// (we never include a trailing '\n' in the WS payload because the receiving
// scanner appends one for us). See internal/transport/ws.go.
//
// Lives in its own go.mod (only dependency: github.com/coder/websocket) so
// it can be built independently of the orchestrator module.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"
)

// dialTimeout caps connection + first-event latency. nous emits the snapshot
// immediately after Hello, so 10s is generous; anything longer means the
// daemon is wedged.
const dialTimeout = 10 * time.Second

// hello mirrors transport.Hello on the daemon side. Field name is fixed by
// the wire format.
type hello struct {
	WorkDir string `json:"work_dir"`
}

// event is the minimal slice of director.Event we need. The wire encoder
// emits a stringified type; "session_state" is what we expect first.
type event struct {
	Type         string `json:"type"`
	SessionState *snapshot `json:"session_state"`
	ErrStr       string `json:"err_str,omitempty"`
}

type snapshot struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	YoloOn    bool   `json:"yolo_on"`
}

type probeResult struct {
	SessionID string `json:"session_id"`
	Directory string `json:"directory"`
	YoloOn    bool   `json:"yolo_on"`
}

func main() {
	addr := flag.String("addr", "ws://localhost:9000/", "WebSocket URL of the nous daemon")
	workDir := flag.String("workdir", "/workspace", "WorkDir to send in the Hello")
	cookie := flag.String("cookie", "", "optional homa_session cookie value (used when probing via the orchestrator's /ws)")
	flag.Parse()

	if err := run(*addr, *workDir, *cookie); err != nil {
		fmt.Fprintf(os.Stderr, "probe: %v\n", err)
		os.Exit(1)
	}
}

func run(addr, workDir, cookie string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	opts := &websocket.DialOptions{}
	if cookie != "" {
		opts.HTTPHeader = http.Header{}
		opts.HTTPHeader.Set("Cookie", "homa_session="+cookie)
	}
	conn, _, err := websocket.Dial(ctx, addr, opts)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(-1)

	helloBytes, err := json.Marshal(hello{WorkDir: workDir})
	if err != nil {
		return fmt.Errorf("marshal hello: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, helloBytes); err != nil {
		return fmt.Errorf("write hello: %w", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("read first event: %w", err)
	}

	var ev event
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unmarshal event: %w (raw=%s)", err, truncate(string(data), 200))
	}
	if ev.Type != "session_state" {
		return fmt.Errorf("expected session_state, got %q (err=%q)", ev.Type, ev.ErrStr)
	}
	if ev.SessionState == nil || ev.SessionState.ID == "" {
		return fmt.Errorf("session_state missing id: %+v", ev.SessionState)
	}

	out, _ := json.Marshal(probeResult{
		SessionID: ev.SessionState.ID,
		Directory: ev.SessionState.Directory,
		YoloOn:    ev.SessionState.YoloOn,
	})
	fmt.Println(string(out))
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
