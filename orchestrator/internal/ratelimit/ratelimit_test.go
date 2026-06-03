package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/skipper/homa/orchestrator/internal/ratelimit"
)

func TestAllow_FreshKeyHasFullBurst(t *testing.T) {
	l := ratelimit.New(3, time.Hour)
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Errorf("call %d denied; want allowed", i+1)
		}
	}
	if l.Allow("k") {
		t.Error("call 4 allowed; want denied (bucket exhausted)")
	}
}

func TestAllow_RefillsOverTime(t *testing.T) {
	// Refill once per 10ms — fast enough to test quickly.
	l := ratelimit.New(2, 10*time.Millisecond)
	if !l.Allow("k") || !l.Allow("k") {
		t.Fatal("burst denied")
	}
	if l.Allow("k") {
		t.Fatal("immediate post-burst allowed; want denied")
	}
	time.Sleep(20 * time.Millisecond) // ≥1 token regenerates
	if !l.Allow("k") {
		t.Error("post-refill denied; want allowed")
	}
}

func TestAllow_PerKeyIsolation(t *testing.T) {
	l := ratelimit.New(1, time.Hour)
	if !l.Allow("a") {
		t.Fatal("a denied")
	}
	// b is fresh — should be allowed independently of a.
	if !l.Allow("b") {
		t.Error("b denied despite a-only consumption")
	}
	if l.Allow("a") {
		t.Error("a's second call allowed; want denied")
	}
}

func TestAllow_ConcurrentAccess(t *testing.T) {
	// Race-detector exercise; no functional assertion beyond no-panic
	// and no data race.
	l := ratelimit.New(100, time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				l.Allow("shared")
			}
		}()
	}
	wg.Wait()
}

// ClientIP tests — extraction priority across X-Forwarded-For, X-Real-IP,
// RemoteAddr.

func TestClientIP_XForwardedForFirst(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")
	r.RemoteAddr = "127.0.0.1:1234"
	if got := ratelimit.ClientIP(r); got != "203.0.113.42" {
		t.Errorf("got %q, want 203.0.113.42", got)
	}
}

func TestClientIP_FallbackToXRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-IP", "203.0.113.42")
	r.RemoteAddr = "127.0.0.1:1234"
	if got := ratelimit.ClientIP(r); got != "203.0.113.42" {
		t.Errorf("got %q, want 203.0.113.42", got)
	}
}

func TestClientIP_FallbackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.42:1234"
	if got := ratelimit.ClientIP(r); got != "203.0.113.42" {
		t.Errorf("got %q, want 203.0.113.42", got)
	}
}

func TestClientIP_IPv6RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8::1]:1234"
	if got := ratelimit.ClientIP(r); got != "[2001:db8::1]" {
		t.Errorf("got %q, want [2001:db8::1]", got)
	}
}

func ExampleLimiter_Allow() {
	l := ratelimit.New(5, time.Hour/5) // 5 per hour
	if l.Allow("192.0.2.1") {
		// proceed
	}
	_ = http.StatusTooManyRequests
}
