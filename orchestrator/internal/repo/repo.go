// Package repo abstracts "a git repository the orchestrator tracks
// + serves PR / merge workflow for". Initial concrete repos:
//
//   "site"     — site-template/. The user-facing SvelteKit site.
//                Per-user worktrees in cfg.BranchesDir/<userid>.
//   "library"  — data/library/. Operator-curated research corpus.
//                Per-user worktrees in cfg.LibraryBranchesDir/<userid>.
//
// Every PR / merge / list operation goes through *Repo so the CLI,
// prflow helpers, and provisioner stay repo-agnostic. Adding a third
// repo later is a single Site()-style constructor + a CLI dispatcher
// entry — no copy-paste of branch-handling code.
package repo

import (
	"fmt"
	"path/filepath"

	"github.com/skipper/homa/orchestrator/internal/config"
)

// Repo captures everything a PR / merge / worktree operation needs to
// know about a git repository the orchestrator manages.
type Repo struct {
	// Name is the short identifier ("site", "library"). Used in CLI
	// dispatch, log fields, and error messages.
	Name string

	// MainDir is the absolute path of the canonical (operator-curated)
	// worktree. Branches live here; `git checkout main && git merge ...`
	// runs here at merge time.
	MainDir string

	// BranchesDir is the absolute path under which per-user worktrees
	// are created (each subdir = one user's worktree, on branch
	// user/<userid>).
	BranchesDir string

	// BaseBranch is the canonical trunk; PRs are diffed and merged
	// against it. Always "main" today; field exists for parity with
	// future repos that might pin a different name.
	BaseBranch string

	// GitBin is the path to the git executable. Plumbed through so
	// tests can swap in a fake.
	GitBin string
}

// Site returns the Repo descriptor for the SvelteKit site-template.
// Pulls all paths from cfg; relative paths are absolutized.
func Site(cfg *config.Config) (*Repo, error) {
	main, err := filepath.Abs(cfg.SiteTemplateDir)
	if err != nil {
		return nil, fmt.Errorf("site MainDir abs: %w", err)
	}
	br, err := filepath.Abs(cfg.BranchesDir)
	if err != nil {
		return nil, fmt.Errorf("site BranchesDir abs: %w", err)
	}
	return &Repo{
		Name:        "site",
		MainDir:     main,
		BranchesDir: br,
		BaseBranch:  "main",
		GitBin:      cfg.GitBin,
	}, nil
}

// Library returns the Repo descriptor for the research library.
// Pulls all paths from cfg; relative paths are absolutized.
func Library(cfg *config.Config) (*Repo, error) {
	main, err := filepath.Abs(cfg.LibraryDir)
	if err != nil {
		return nil, fmt.Errorf("library MainDir abs: %w", err)
	}
	br, err := filepath.Abs(cfg.LibraryBranchesDir)
	if err != nil {
		return nil, fmt.Errorf("library BranchesDir abs: %w", err)
	}
	return &Repo{
		Name:        "library",
		MainDir:     main,
		BranchesDir: br,
		BaseBranch:  "main",
		GitBin:      cfg.GitBin,
	}, nil
}

// UserWorktreeDir returns the absolute path to the per-user worktree
// for the given userid under this repo.
func (r *Repo) UserWorktreeDir(userID string) string {
	return filepath.Join(r.BranchesDir, userID)
}

// UserBranch returns the branch name pinned to a user's worktree under
// this repo. Matches the existing site-template convention (user/<id>).
func (r *Repo) UserBranch(userID string) string {
	return "user/" + userID
}
