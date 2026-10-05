package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitBranch(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := GitBranch(sub); got != "" {
		t.Fatalf("outside a repository: %q", got)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(root, ".git", "HEAD")
	_ = os.WriteFile(head, []byte("ref: refs/heads/feat/x\n"), 0o644)
	if got := GitBranch(sub); got != "feat/x" {
		t.Fatalf("branch: %q", got)
	}
	_ = os.WriteFile(head, []byte("0123456789abcdef\n"), 0o644)
	if got := GitBranch(sub); got != "HEAD" {
		t.Fatalf("detached: %q", got)
	}
}
