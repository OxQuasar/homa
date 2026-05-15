// Package provision is the seam between auth and the per-user sandbox.
//
// StubProvisioner returns deterministic placeholder values (no filesystem /
// git / podman / tailscale side effects); it's the default and is used by
// every unit / e2e test. PodmanProvisioner (podman_provisioner.go)
// implements the real flow per mvp.md §9 behind the same interface.
package provision

import (
	"context"
	"fmt"
	"path/filepath"
)

// Port allocation starting points come from mvp.md §2:
//   - host ports for nous + preview begin at 40000 (shared monotonic counter)
//   - tailscale-serve HTTPS ports begin at 10001
const (
	HostPortStart         = 40000
	PreviewServePortStart = 10001
)

// Result is the set of fields a Provisioner produces for a freshly created
// user. Every field maps 1:1 onto a NOT NULL column on the users table.
//
// PreviewURL is *derived* from PreviewBaseURL + PreviewServePort. It's NOT
// persisted (it would drift when the base URL changes); auth.Service
// re-derives it on /me responses. Kept here so provisioners that synthesise
// the URL once (e.g. PodmanProvisioner consulting a config base) don't have
// to plumb the base through auth.
type Result struct {
	BranchName       string
	WorktreePath     string
	ContainerName    string
	NousPort         int
	PreviewPort      int
	PreviewServePort int
	PreviewURL       string // derived, not persisted; auth re-derives for /me
}

// Provisioner is implemented by anything that can stand up a per-user sandbox
// (PodmanProvisioner) or return placeholder values for it (StubProvisioner).
type Provisioner interface {
	// Provision creates a brand-new sandbox for a freshly-signed-up user,
	// allocating ports and persisting all the NOT NULL fields. Called once
	// per user from auth.Signup.
	Provision(ctx context.Context, userID string) (Result, error)

	// EnsureRunning brings the user's existing sandbox back up if it isn't
	// already running. Idempotent: no-op when the container is up. Called
	// on every successful Login (mvp.md §10 step 2) and on lifecycle
	// respawn events. Must NOT allocate new ports — the user's row already
	// carries them. Stub returns nil (no real container).
	EnsureRunning(ctx context.Context, userID string) error
}

// StubProvisioner returns deterministic placeholder values for the six NOT
// NULL user columns. No filesystem, git, podman, or tailscale side effects.
type StubProvisioner struct {
	branchesDir string
	ports       *PortAllocator
}

// NewStubProvisioner returns a stub against branchesDir with default port
// starts (provision.HostPortStart / provision.PreviewServePortStart).
func NewStubProvisioner(branchesDir string) *StubProvisioner {
	return NewStubProvisionerWithStarts(branchesDir, HostPortStart, PreviewServePortStart)
}

// NewStubProvisionerWithStarts overrides the port-start values. Used in
// tests + the e2e script so the next signup's nous_port lands on a known
// fake-upstream listener.
func NewStubProvisionerWithStarts(branchesDir string, hostStart, previewServeStart int) *StubProvisioner {
	return &StubProvisioner{
		branchesDir: branchesDir,
		ports:       NewPortAllocator(hostStart, previewServeStart),
	}
}

// NewStubFromAllocator wires a stub against a pre-built (possibly pre-seeded)
// allocator. main.go uses this so the same allocator instance is shared
// between Stub and Podman code paths and survives restart-rehydration.
func NewStubFromAllocator(branchesDir string, ports *PortAllocator) *StubProvisioner {
	return &StubProvisioner{branchesDir: branchesDir, ports: ports}
}

// EnsureRunning is a no-op for the stub — there's nothing to ensure.
func (p *StubProvisioner) EnsureRunning(_ context.Context, _ string) error { return nil }

// Provision returns placeholder values for the user. It does not touch disk,
// run git, start containers, or invoke tailscale.
func (p *StubProvisioner) Provision(_ context.Context, userID string) (Result, error) {
	if userID == "" {
		return Result{}, fmt.Errorf("provision: empty userID")
	}
	nousPort := p.ports.NextHostPort()
	previewPort := p.ports.NextHostPort()
	previewServePort := p.ports.NextServePort()
	return Result{
		BranchName:       "user/" + userID,
		WorktreePath:     filepath.Join(p.branchesDir, userID),
		ContainerName:    "homa-user-" + userID,
		NousPort:         nousPort,
		PreviewPort:      previewPort,
		PreviewServePort: previewServePort,
	}, nil
}
