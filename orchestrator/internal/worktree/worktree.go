// Package worktree wraps `git worktree` for the per-user fork-from-main
// flow described in mvp.md §9 step 5.
package worktree

import (
	"context"
	"fmt"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
)

// baseBranch is the source branch every user is forked from.
const baseBranch = "main"

// defaultBin is the binary name used when the caller supplies an empty string.
const defaultBin = "git"

// Service manages git worktrees for individual users.
type Service interface {
	// Create runs `git -C repoDir worktree add worktreePath -b branchName main`.
	Create(ctx context.Context, repoDir, branchName, worktreePath string) error

	// Remove tears down a worktree (force, so dirty state doesn't block GC).
	Remove(ctx context.Context, repoDir, worktreePath string) error
}

// CLI is the production Service backed by the host's `git` via sandbox.Runner.
type CLI struct {
	Bin    string
	Runner sandbox.Runner
}

// New builds a worktree.CLI. Empty bin → "git"; nil runner → ExecRunner{}.
func New(bin string, runner sandbox.Runner) *CLI {
	if bin == "" {
		bin = defaultBin
	}
	if runner == nil {
		runner = sandbox.ExecRunner{}
	}
	return &CLI{Bin: bin, Runner: runner}
}

func (c *CLI) Create(ctx context.Context, repoDir, branchName, worktreePath string) error {
	args := []string{"-C", repoDir, "worktree", "add", worktreePath, "-b", branchName, baseBranch}
	if _, err := c.Runner.Run(ctx, c.Bin, args...); err != nil {
		return fmt.Errorf("git worktree add %s (%s): %w", worktreePath, branchName, err)
	}
	return nil
}

func (c *CLI) Remove(ctx context.Context, repoDir, worktreePath string) error {
	args := []string{"-C", repoDir, "worktree", "remove", "--force", worktreePath}
	if _, err := c.Runner.Run(ctx, c.Bin, args...); err != nil {
		return fmt.Errorf("git worktree remove %s: %w", worktreePath, err)
	}
	return nil
}
