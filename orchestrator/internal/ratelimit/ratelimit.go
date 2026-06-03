// Package ratelimit implements a per-key token bucket — used to throttle
// abuse-prone endpoints (signup, login) at the HTTP layer.
//
// In-memory only. State resets on orchestrator restart; that's fine for
// signup-class limits (which exist to defeat bots, not to enforce SLAs).
// A janitor goroutine evicts idle buckets so memory stays bounded.
package ratelimit

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter is a goroutine-safe per-key token bucket. Each key gets its
// own bucket; the bucket refills at a constant rate up to a cap.
type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity int           // max tokens a bucket holds (== burst allowance)
	refill   time.Duration // time to gain one token
	idle     time.Duration // after this much idleness, the bucket is GC'd
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

// New constructs a Limiter. capacity is the burst (e.g. 5 = a fresh
// key can fire 5 calls instantly). refill is how often one token
// regenerates (e.g. 12*time.Minute → 5/hour). idle is the GC threshold
// for unused keys; default 1h when zero.
func New(capacity int, refill time.Duration) *Limiter {
	l := &Limiter{
		buckets:  map[string]*bucket{},
		capacity: capacity,
		refill:   refill,
		idle:     time.Hour,
	}
	go l.gcLoop()
	return l
}

// Allow consumes one token from key's bucket. Returns true if the
// action is allowed, false if rate-limited. Thread-safe.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	now := time.Now()
	if !ok {
		// Fresh key — fully provisioned.
		l.buckets[key] = &bucket{tokens: float64(l.capacity - 1), lastRefill: now}
		return true
	}
	// Refill based on time elapsed since last touch.
	elapsed := now.Sub(b.lastRefill)
	if l.refill > 0 {
		add := float64(elapsed) / float64(l.refill)
		b.tokens += add
		if b.tokens > float64(l.capacity) {
			b.tokens = float64(l.capacity)
		}
	}
	b.lastRefill = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gcLoop runs forever, periodically evicting buckets that haven't been
// touched in `idle`. Cheap; runs once per idle period.
func (l *Limiter) gcLoop() {
	ticker := time.NewTicker(l.idle)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-l.idle)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.lastRefill.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// ClientIP extracts the originating client IP from an HTTP request.
// Strategy:
//
//   1. X-Forwarded-For: first comma-separated entry (closest to client).
//      Tailscale Serve / reverse proxies set this on forwarded requests.
//   2. X-Real-IP: single-value header some proxies use instead.
//   3. RemoteAddr: TCP-level peer, less reliable behind any proxy.
//
// Returns "" only if no signal is available (defensive — most code
// should treat empty as "rate-limit-everything").
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	if i := strings.LastIndex(r.RemoteAddr, ":"); i >= 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
