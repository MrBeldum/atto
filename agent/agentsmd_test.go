package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project makes <tmp>/repo/a/b with a fresh ATTO_DIR; the caller adds .git.
func project(t *testing.T) (repo, cwd, attoDir string) {
	t.Helper()
	tmp, _ := filepath.EvalSymlinks(t.TempDir())
	attoDir = filepath.Join(tmp, "atto")
	t.Setenv("ATTO_DIR", attoDir)
	repo = filepath.Join(tmp, "repo")
	cwd = filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func paths(fs []instructionFile) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.path)
	}
	return out
}

func TestInstructionsRootWalkAndOrder(t *testing.T) {
	repo, cwd, dir := project(t)
	write(t, filepath.Join(repo, ".git", "HEAD"), "x")
	write(t, filepath.Join(dir, "AGENTS.md"), "global")
	write(t, filepath.Join(repo, "AGENTS.md"), "root")
	write(t, filepath.Join(cwd, "AGENTS.md"), "leaf")
	// Above the root: must not be read.
	write(t, filepath.Join(filepath.Dir(repo), "AGENTS.md"), "above")

	got := paths(loadInstructions(cwd))
	want := []string{filepath.Join(dir, "AGENTS.md"), filepath.Join(repo, "AGENTS.md"), filepath.Join(cwd, "AGENTS.md")}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestInstructionsGitFile(t *testing.T) {
	repo, cwd, _ := project(t)
	write(t, filepath.Join(repo, ".git"), "gitdir: /elsewhere") // worktree style
	write(t, filepath.Join(repo, "CLAUDE.md"), "root")
	write(t, filepath.Join(filepath.Dir(repo), "AGENTS.md"), "above")
	got := paths(loadInstructions(cwd))
	if len(got) != 1 || got[0] != filepath.Join(repo, "CLAUDE.md") {
		t.Fatalf("got %v", got)
	}
}

func TestInstructionsNoGitUsesCwdOnly(t *testing.T) {
	repo, cwd, _ := project(t)
	write(t, filepath.Join(repo, "AGENTS.md"), "parent")
	write(t, filepath.Join(cwd, "AGENTS.md"), "leaf")
	got := paths(loadInstructions(cwd))
	if len(got) != 1 || got[0] != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("got %v", got)
	}
}

func TestInstructionsPrecedence(t *testing.T) {
	repo, cwd, dir := project(t)
	write(t, filepath.Join(repo, ".git"), "")
	for _, n := range []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"} {
		write(t, filepath.Join(dir, n), "g")
		write(t, filepath.Join(cwd, n), "p")
	}
	got := paths(loadInstructions(cwd))
	if len(got) != 2 || filepath.Base(got[0]) != "AGENTS.override.md" || filepath.Base(got[1]) != "AGENTS.override.md" {
		t.Fatalf("got %v", got)
	}
	os.Remove(filepath.Join(dir, "AGENTS.override.md"))
	os.Remove(filepath.Join(cwd, "AGENTS.override.md"))
	os.Remove(filepath.Join(cwd, "AGENTS.md"))
	got = paths(loadInstructions(cwd))
	if filepath.Base(got[0]) != "AGENTS.md" || filepath.Base(got[1]) != "CLAUDE.md" {
		t.Fatalf("fallback: got %v", got)
	}
}

func TestInstructionsCap(t *testing.T) {
	repo, cwd, dir := project(t)
	write(t, filepath.Join(repo, ".git"), "")
	write(t, filepath.Join(dir, "AGENTS.md"), strings.Repeat("g", 20*1024))
	write(t, filepath.Join(repo, "AGENTS.md"), strings.Repeat("r", 20*1024))
	write(t, filepath.Join(cwd, "AGENTS.md"), "leaf")
	var b strings.Builder
	writeInstructions(&b, loadInstructions(cwd))
	out := b.String()
	if n := strings.Count(out, "r"); n < 12*1024 || n > 12*1024+200 { // path and note add a few r's
		t.Fatalf("root file kept %d r's, want about 12 KiB", n)
	}
	if !strings.Contains(out, "Truncated") || strings.Contains(out, "leaf") {
		t.Fatalf("expected truncation note and dropped leaf")
	}
	if len(out) > maxInstructionBytes+1024 {
		t.Fatalf("output %d bytes over cap", len(out))
	}
}

func TestInstructionsNoFilesNoChange(t *testing.T) {
	_, cwd, _ := project(t)
	var b strings.Builder
	writeInstructions(&b, loadInstructions(cwd))
	if b.Len() != 0 {
		t.Fatalf("got %q", b.String())
	}
}
