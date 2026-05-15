package worktree_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/sandbox"
	"github.com/skipper/homa/orchestrator/internal/sandbox/runnertest"
	"github.com/skipper/homa/orchestrator/internal/worktree"
)

// TestCreateEmitsExpectedArgv — exact argv per spec.
func TestCreateEmitsExpectedArgv(t *testing.T) {
	fr := &runnertest.FakeRunner{}
	svc := worktree.New("git", fr)
	if err := svc.Create(context.Background(), "/srv/template", "user/abcd1234", "/srv/branches/abcd1234"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	calls := fr.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "git" {
		t.Errorf("bin: got %q, want git", calls[0].Name)
	}
	wantArgs := []string{
		"-C", "/srv/template",
		"worktree", "add", "/srv/branches/abcd1234",
		"-b", "user/abcd1234",
		"main",
	}
	if !reflect.DeepEqual(calls[0].Args, wantArgs) {
		t.Errorf("args:\n got  %v\n want %v", calls[0].Args, wantArgs)
	}
}

func TestRemoveEmitsExpectedArgv(t *testing.T) {
	fr := &runnertest.FakeRunner{}
	svc := worktree.New("git", fr)
	if err := svc.Remove(context.Background(), "/srv/template", "/srv/branches/abcd1234"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	calls := fr.Calls()
	wantArgs := []string{"-C", "/srv/template", "worktree", "remove", "--force", "/srv/branches/abcd1234"}
	if !reflect.DeepEqual(calls[0].Args, wantArgs) {
		t.Errorf("args:\n got  %v\n want %v", calls[0].Args, wantArgs)
	}
}

// TestRealGitWorktreeRoundtrip exercises the real git binary on a temp repo.
// Git is on every host this orchestrator targets — unlike podman/tailscale.
func TestRealGitWorktreeRoundtrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := filepath.Join(t.TempDir(), "template")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mustRun := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	// Bootstrap a repo with a single commit on `main`.
	mustRun("git", "init", "-b", "main")
	mustRun("git", "config", "user.email", "test@x.io")
	mustRun("git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRun("git", "add", ".")
	mustRun("git", "commit", "-m", "init")

	svc := worktree.New("git", sandbox.ExecRunner{})
	wtPath := filepath.Join(t.TempDir(), "branches", "abcd1234")

	ctx := context.Background()
	if err := svc.Create(ctx, repo, "user/abcd1234", wtPath); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Worktree dir exists and contains the README from `main`.
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(wtPath, "README.md")); err != nil || string(b) != "hi\n" {
		t.Errorf("README in worktree: got %q (err=%v), want %q", b, err, "hi\n")
	}

	if err := svc.Remove(ctx, repo, wtPath); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after Remove (err=%v)", err)
	}
}
