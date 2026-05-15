package provision

import "sync"

// PortAllocator hands out monotonically-increasing host + tailscale-serve
// ports. Process-global by convention (one instance per orchestrator). Safe
// for concurrent use; mutex-protected.
//
// Restart survival: there is NO separate watermark table. The `users` table
// is the source of truth. cmd/homa/main.go calls store.AllUserPorts() at
// startup and (*PortAllocator).Seed() bumps the in-memory counter past the
// existing max, guaranteeing the next allocation can't collide with any
// previously-issued port.
type PortAllocator struct {
	mu        sync.Mutex
	nextHost  int
	nextServe int
}

// NewPortAllocator returns an allocator pre-positioned at the given starts.
func NewPortAllocator(hostStart, serveStart int) *PortAllocator {
	return &PortAllocator{nextHost: hostStart, nextServe: serveStart}
}

// NextHostPort returns one port and bumps the counter by 1.
func (p *PortAllocator) NextHostPort() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	port := p.nextHost
	p.nextHost++
	return port
}

// NextServePort returns one tailscale-serve port and bumps the counter.
func (p *PortAllocator) NextServePort() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	port := p.nextServe
	p.nextServe++
	return port
}

// Seed bumps the counters to past the highest port we've already handed out
// historically. Called on daemon startup with the union of every port found
// in the users table — guarantees no second-user-after-restart port
// collision.
//
// Idempotent + monotone: a smaller-than-current seed is a no-op.
func (p *PortAllocator) Seed(usedHostPorts, usedServePorts []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, port := range usedHostPorts {
		if port >= p.nextHost {
			p.nextHost = port + 1
		}
	}
	for _, port := range usedServePorts {
		if port >= p.nextServe {
			p.nextServe = port + 1
		}
	}
}
