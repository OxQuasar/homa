package prflow

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Stats summarizes a PR branch's diff vs the target branch (typically
// main). Surfaced in `homa pr list` / `homa pr show`.
type Stats struct {
	CommitsAhead int
	FilesChanged int
	Insertions   int
	Deletions    int
}

// PR is a list-row: parsed branch + stats. Listing fills both.
type PR struct {
	PRBranch
	Stats Stats
}

// List enumerates every branch under repoDir whose name parses as a
// PR branch. For each, computes Stats vs targetBranch (typically
// "main"). Output is unsorted; callers can sort how they like.
//
// Shells out to git directly; if you wanted to swap for a libgit2
// or go-git implementation, this is the surface.
func List(gitBin, repoDir, targetBranch string) ([]PR, error) {
	// `git branch --list 'pr/*' --format='%(refname:short)'` is the
	// stable way to enumerate matching branches.
	out, err := runGit(gitBin, repoDir, "branch", "--list", "pr/*", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var prs []PR
	for _, line := range splitLines(string(out)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b, ok := ParsePRBranch(line)
		if !ok {
			continue
		}
		s, err := DiffStats(gitBin, repoDir, b.Name, targetBranch)
		if err != nil {
			// Skip this branch, surface the error via log not panic.
			// Most likely cause: target branch doesn't exist yet.
			continue
		}
		prs = append(prs, PR{PRBranch: b, Stats: s})
	}
	return prs, nil
}

// DiffStats returns commits-ahead + file/line diff stats for source
// vs target. Two git invocations:
//   1. `git rev-list --count target..source`  (commits ahead)
//   2. `git diff --shortstat target...source` (files/insertions/deletions)
//
// `...` (triple-dot) in `git diff` shows what `source` introduces
// relative to the merge base — what a merge of source into target
// would change.
func DiffStats(gitBin, repoDir, source, target string) (Stats, error) {
	var s Stats

	// 1. commits ahead
	rc, err := runGit(gitBin, repoDir, "rev-list", "--count", fmt.Sprintf("%s..%s", target, source))
	if err != nil {
		return s, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(rc)))
	if err != nil {
		return s, fmt.Errorf("parse rev-list count %q: %w", string(rc), err)
	}
	s.CommitsAhead = n

	// 2. shortstat
	stat, err := runGit(gitBin, repoDir, "diff", "--shortstat",
		fmt.Sprintf("%s...%s", target, source))
	if err != nil {
		return s, err
	}
	// shortstat output: " 5 files changed, 120 insertions(+), 45 deletions(-)"
	// All three numbers may be absent; parse defensively.
	s.FilesChanged, s.Insertions, s.Deletions = parseShortStat(string(stat))
	return s, nil
}

// parseShortStat extracts the three integers from `git diff --shortstat`
// output. Any missing number is 0. Stable against rewordings like
// "1 file changed" vs "5 files changed".
func parseShortStat(raw string) (files, ins, del int) {
	for _, part := range strings.Split(raw, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(fields[1], "file"):
			files = n
		case strings.HasPrefix(fields[1], "insertion"):
			ins = n
		case strings.HasPrefix(fields[1], "deletion"):
			del = n
		}
	}
	return
}

// CommitLog returns recent commit summaries on source not yet in
// target — used by `homa pr show`. Format: "abc1234 short subject".
func CommitLog(gitBin, repoDir, source, target string) ([]string, error) {
	out, err := runGit(gitBin, repoDir, "log",
		fmt.Sprintf("%s..%s", target, source),
		"--oneline", "--no-decorate", "--no-color")
	if err != nil {
		return nil, err
	}
	return splitLines(string(out)), nil
}

// FilesChangedList returns the per-file shortstat ('numstat' format)
// for source vs target — for the `homa pr show` file list.
//
// numstat output: "<ins>\t<del>\t<file>" per line, with binary files
// showing "-\t-\t<file>".
func FilesChangedList(gitBin, repoDir, source, target string) ([]string, error) {
	out, err := runGit(gitBin, repoDir, "diff", "--numstat",
		fmt.Sprintf("%s...%s", target, source))
	if err != nil {
		return nil, err
	}
	return splitLines(string(out)), nil
}

// BranchExists reports whether a branch ref exists in repoDir.
// `git rev-parse --verify` returns non-zero on missing.
func BranchExists(gitBin, repoDir, branch string) bool {
	_, err := runGit(gitBin, repoDir, "rev-parse", "--verify",
		"--quiet", "refs/heads/"+branch)
	return err == nil
}

// DeleteBranch deletes a branch with -D (force, since branches not
// merged into HEAD would otherwise be rejected). Used by `pr close`.
func DeleteBranch(gitBin, repoDir, branch string) error {
	_, err := runGit(gitBin, repoDir, "branch", "-D", branch)
	return err
}

// runGit invokes git with cwd=repoDir; stderr is merged into the
// returned error context.
func runGit(gitBin, repoDir string, args ...string) ([]byte, error) {
	cmd := exec.Command(gitBin, append([]string{"-C", repoDir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w (stderr: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
