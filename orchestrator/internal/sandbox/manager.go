package sandbox

import "context"

// Spec is the complete description of a per-user sandbox container.
// All fields are required; defaults live in config, not here.
type Spec struct {
	ContainerName string            // e.g. "homa-user-abcd1234"
	ImageRef      string            // e.g. "homa-sandbox:latest"
	WorktreePath  string            // absolute host path bind-mounted at /workspace
	NousPort      int               // host port → container :9000 (nous WS)
	PreviewPort   int               // host port → container :5173 (vite dev)
	MemoryLimit   string            // e.g. "2g"
	CPULimit      string            // e.g. "2"
	Env           map[string]string // injected into the container, e.g. ANTHROPIC_API_KEY
}

// Manager owns the lifecycle of per-user sandbox containers.
// Implementations are expected to be idempotent and concurrency-safe.
type Manager interface {
	// Ensure makes sure a container matching spec is running. If one is
	// already running by that name, Ensure is a no-op.
	Ensure(ctx context.Context, spec Spec) error

	// Stop terminates the container if present. Idempotent: absent
	// container is not an error.
	Stop(ctx context.Context, containerName string) error

	// IsRunning reports whether a container with the given name is in the
	// "running" state. An absent container is `(false, nil)`. Errors
	// other than the absent-container case bubble up.
	IsRunning(ctx context.Context, containerName string) (bool, error)

	// Logs returns the last `lines` lines of the container's log output.
	Logs(ctx context.Context, containerName string, lines int) ([]string, error)
}
