// Package prflow implements the git-native PR convention:
//
//   pr/<userid>/<topic>  — a branch the user / LLM creates to stage
//                          work for review before merging into main.
//
// Phase 1: branches are the only artifact (no DB metadata).
// `homa pr list` enumerates them, `homa pr merge <branch>` is the
// review-then-promote path. Phase 2 (later) layers a pull_requests
// table for titles, descriptions, comments, etc.
//
// Pure helpers (ParsePRBranch + validation) live here so they're
// trivially testable. Git shell-outs live in git.go alongside them.
package prflow

import (
	"regexp"
)

// PRBranch is a parsed PR branch ref.
type PRBranch struct {
	Name   string // "pr/77b4cf0e/dark-mode"
	UserID string // "77b4cf0e"
	Topic  string // "dark-mode"
}

// Convention constants — single source of truth for the prefix +
// component charsets. Branch names must match BranchPattern as a
// whole; the userID + topic groups are extracted from match groups.
const (
	BranchPrefix = "pr/"
)

// branchPattern matches `pr/<userid>/<topic>` where:
//   userid: 8 lowercase hex chars (matches the userIDBytes=4 in auth)
//   topic:  [a-zA-Z0-9._-]+ (loose; URL-safe charset, no slashes — a
//           slash would imply nesting, ambiguous with this layout)
var branchPattern = regexp.MustCompile(`^pr/([a-f0-9]{8})/([a-zA-Z0-9._-]+)$`)

// ParsePRBranch returns the parsed components and a flag indicating
// whether the name matched. False return means: not a PR branch under
// this convention — skip / ignore.
func ParsePRBranch(name string) (PRBranch, bool) {
	m := branchPattern.FindStringSubmatch(name)
	if m == nil {
		return PRBranch{}, false
	}
	return PRBranch{Name: name, UserID: m[1], Topic: m[2]}, true
}

// IsPRBranch is the bool-only variant of ParsePRBranch, for call sites
// that don't need the components.
func IsPRBranch(name string) bool {
	_, ok := ParsePRBranch(name)
	return ok
}
