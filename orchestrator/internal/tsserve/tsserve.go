// Package tsserve wraps `tailscale serve` for HTTPS mappings from the
// tailnet down to user preview ports on localhost.
//
// Per mvp.md §15: orchestrator runs `tailscale serve --bg --https=<port>
// http://localhost:<previewPort>` on signup; idempotent across respawns.
package tsserve

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
)

// defaultBin is the binary name used when the caller supplies an empty string.
const defaultBin = "tailscale"

// Service registers per-user tailscale-serve HTTPS mappings.
type Service interface {
	Register(ctx context.Context, servePort int, target string) error
	Unregister(ctx context.Context, servePort int) error
	IsRegistered(ctx context.Context, servePort int) (bool, error)
}

// CLI is the production Service backed by `tailscale` via sandbox.Runner.
type CLI struct {
	Bin    string
	Runner sandbox.Runner
}

// New builds a tsserve.CLI. Empty bin defaults to "tailscale"; nil runner
// defaults to sandbox.ExecRunner{}.
func New(bin string, runner sandbox.Runner) *CLI {
	if bin == "" {
		bin = defaultBin
	}
	if runner == nil {
		runner = sandbox.ExecRunner{}
	}
	return &CLI{Bin: bin, Runner: runner}
}

// Register adds `--https=<servePort>` → target. Idempotent — if the mapping
// already exists, the underlying `tailscale serve` accepts the re-register.
func (c *CLI) Register(ctx context.Context, servePort int, target string) error {
	args := []string{"serve", "--bg", "--https=" + strconv.Itoa(servePort), target}
	if _, err := c.Runner.Run(ctx, c.Bin, args...); err != nil {
		return fmt.Errorf("tailscale serve register :%d → %s: %w", servePort, target, err)
	}
	return nil
}

// Unregister removes the mapping on `servePort`. Idempotent — a missing
// mapping is not an error.
func (c *CLI) Unregister(ctx context.Context, servePort int) error {
	args := []string{"serve", "--https=" + strconv.Itoa(servePort), "off"}
	if _, err := c.Runner.Run(ctx, c.Bin, args...); err != nil {
		// "no serve config to remove" surfaces as non-zero exit; tolerate.
		if sandbox.IsExitError(err) {
			return nil
		}
		return fmt.Errorf("tailscale serve unregister :%d: %w", servePort, err)
	}
	return nil
}

// IsRegistered queries `tailscale serve status --json` and substring-matches
// the port. We don't fully parse the JSON — the captain specified minimal
// detection and tailscale's status JSON schema has shifted across versions.
// The key shape is typically "host:port" (e.g. "device.tailnet.ts.net:443"),
// so we look for `:<port>` anywhere in the output — tight enough to avoid
// matching plain digits in `Handlers` values.
func (c *CLI) IsRegistered(ctx context.Context, servePort int) (bool, error) {
	out, err := c.Runner.Run(ctx, c.Bin, "serve", "status", "--json")
	if err != nil {
		return false, fmt.Errorf("tailscale serve status: %w", err)
	}
	needle := ":" + strconv.Itoa(servePort)
	return strings.Contains(string(out), needle), nil
}
