package sandboxstatus_test

import (
	"sync"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/sandboxstatus"
)

func TestUnknownUserDefaultsToReady(t *testing.T) {
	tr := sandboxstatus.New()
	got := tr.Get("nope")
	if got.Status != sandboxstatus.StatusReady {
		t.Errorf("unknown user: got %q, want ready", got.Status)
	}
	if got.Message != "" {
		t.Errorf("unknown user: got message %q, want empty", got.Message)
	}
}

func TestLifecycle(t *testing.T) {
	tr := sandboxstatus.New()

	tr.MarkStarting("u1")
	if got := tr.Get("u1").Status; got != sandboxstatus.StatusStarting {
		t.Errorf("after MarkStarting: got %q, want starting", got)
	}

	tr.MarkReady("u1")
	if got := tr.Get("u1").Status; got != sandboxstatus.StatusReady {
		t.Errorf("after MarkReady: got %q, want ready", got)
	}

	tr.MarkFailed("u1", "Anthropic creds expired")
	got := tr.Get("u1")
	if got.Status != sandboxstatus.StatusFailed {
		t.Errorf("after MarkFailed: status %q", got.Status)
	}
	if got.Message != "Anthropic creds expired" {
		t.Errorf("message: %q", got.Message)
	}

	// Retry → MarkStarting clears the failed state.
	tr.MarkStarting("u1")
	got = tr.Get("u1")
	if got.Status != sandboxstatus.StatusStarting {
		t.Errorf("retry: status %q", got.Status)
	}
	if got.Message != "" {
		t.Errorf("retry: stale message %q remained", got.Message)
	}
}

func TestPerUserIsolation(t *testing.T) {
	tr := sandboxstatus.New()
	tr.MarkStarting("alice")
	tr.MarkFailed("bob", "out of disk")

	if s := tr.Get("alice"); s.Status != sandboxstatus.StatusStarting {
		t.Errorf("alice: %v", s)
	}
	if s := tr.Get("bob"); s.Status != sandboxstatus.StatusFailed {
		t.Errorf("bob: %v", s)
	}
	if s := tr.Get("carol"); s.Status != sandboxstatus.StatusReady {
		t.Errorf("unknown carol: %v", s)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tr := sandboxstatus.New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "user-" + string(rune('a'+i%26))
			tr.MarkStarting(id)
			tr.MarkReady(id)
			_ = tr.Get(id)
		}(i)
	}
	wg.Wait()
	// No assertion — the test is data-race protection. Run with -race.
}
