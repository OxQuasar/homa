package provision

import (
	"sort"
	"sync"
	"testing"
)

// TestPortAllocatorConcurrency — N goroutines pulling host + serve ports.
// All values must be unique and monotone post-sort.
func TestPortAllocatorConcurrency(t *testing.T) {
	const n = 100
	pa := NewPortAllocator(40000, 10001)

	hosts := make([]int, n)
	serves := make([]int, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			hosts[i] = pa.NextHostPort()
			serves[i] = pa.NextServePort()
		}(i)
	}
	wg.Wait()

	if dups := findDuplicates(hosts); len(dups) > 0 {
		t.Errorf("duplicate host ports: %v", dups)
	}
	if dups := findDuplicates(serves); len(dups) > 0 {
		t.Errorf("duplicate serve ports: %v", dups)
	}

	sort.Ints(hosts)
	if hosts[0] != 40000 || hosts[n-1] != 40000+n-1 {
		t.Errorf("host range: got [%d..%d], want [40000..%d]", hosts[0], hosts[n-1], 40000+n-1)
	}
	sort.Ints(serves)
	if serves[0] != 10001 || serves[n-1] != 10001+n-1 {
		t.Errorf("serve range: got [%d..%d], want [10001..%d]", serves[0], serves[n-1], 10001+n-1)
	}
}

func findDuplicates(xs []int) []int {
	seen := map[int]bool{}
	var dups []int
	for _, x := range xs {
		if seen[x] {
			dups = append(dups, x)
		}
		seen[x] = true
	}
	return dups
}

// TestPortAllocatorSeed — operational fix #1: PortAllocator survives
// orchestrator restart by rehydrating from the users table.
func TestPortAllocatorSeed(t *testing.T) {
	pa := NewPortAllocator(40000, 10001)
	pa.Seed([]int{40050, 40051}, []int{10003})
	if got := pa.NextHostPort(); got != 40052 {
		t.Errorf("post-seed NextHostPort: got %d, want 40052", got)
	}
	if got := pa.NextServePort(); got != 10004 {
		t.Errorf("post-seed NextServePort: got %d, want 10004", got)
	}

	// Re-seeding with smaller values must be a no-op (monotone).
	pa.Seed([]int{40010}, []int{10002})
	if got := pa.NextHostPort(); got != 40053 {
		t.Errorf("after smaller seed NextHostPort: got %d, want 40053", got)
	}

	// Empty seed leaves counters untouched.
	prevHost := pa.NextHostPort()
	pa.Seed(nil, nil)
	if got := pa.NextHostPort(); got != prevHost+1 {
		t.Errorf("empty seed should not perturb counter: prev=%d, got %d", prevHost, got)
	}
}
