// Package sandboxstatus tracks the bring-up state of per-user sandboxes
// so the editor can show a loading screen instead of waiting silently
// while EnsureRunning works.
//
// Default state for an unknown user is "ready" (meaning: no startup
// is in progress, container is presumed up). The /login handler calls
// MarkStarting → spawns the EnsureRunning goroutine → calls MarkReady
// or MarkFailed when it returns. The editor polls /me/sandbox to
// know when to swap from the loading screen to the chat UI.
package sandboxstatus

import (
	"sync"
)

// Status enum on the wire. Editor consumes these strings directly.
type Status string

const (
	// StatusReady — container is up (or was never marked as starting).
	// Editor proceeds to the normal chat UI.
	StatusReady Status = "ready"
	// StatusStarting — a goroutine is currently running EnsureRunning.
	// Editor shows the loading screen.
	StatusStarting Status = "starting"
	// StatusFailed — EnsureRunning returned an error.
	// Editor shows the failure UI with retry.
	StatusFailed Status = "failed"
)

// State is the wire shape returned by Get.
type State struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"` // populated only on failed
}

// Tracker is the per-orchestrator in-memory state map. Thread-safe.
// Outlives individual login goroutines; a transient `failed` becomes
// `ready` when the user next retries (login spawns a new MarkStarting).
type Tracker struct {
	mu     sync.RWMutex
	states map[string]State
}

// New constructs an empty Tracker.
func New() *Tracker {
	return &Tracker{states: map[string]State{}}
}

// Get returns the current state for userID. Unknown users return
// {StatusReady, ""} — the editor's loading screen treats that as
// "no startup in progress, container is presumed up". Lets the
// orchestrator restart without losing pre-existing-up state.
func (t *Tracker) Get(userID string) State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.states[userID]; ok {
		return s
	}
	return State{Status: StatusReady}
}

// MarkStarting records that an EnsureRunning goroutine has begun for
// userID. Idempotent — overwrites any prior failed/ready state.
func (t *Tracker) MarkStarting(userID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states[userID] = State{Status: StatusStarting}
}

// MarkReady records successful container bring-up.
func (t *Tracker) MarkReady(userID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states[userID] = State{Status: StatusReady}
}

// MarkFailed records a bring-up failure. msg is shown verbatim in
// the editor; keep it user-actionable (e.g. mention claude login)
// rather than dumping a Go error string.
func (t *Tracker) MarkFailed(userID, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states[userID] = State{Status: StatusFailed, Message: msg}
}
