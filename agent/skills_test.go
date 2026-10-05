package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/shell"
)

func TestSystemPromptSkills(t *testing.T) {
	repo, cwd, dir := project(t)
	home := filepath.Join(filepath.Dir(repo), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	write(t, filepath.Join(repo, ".git"), "")

	sh := shell.Default()
	start := time.Date(2026, 1, 2, 3, 0, 0, 0, time.Local)
	a := &Agent{Cwd: cwd, Shell: sh}

	// Without skills the prompt is exactly the prompt of a build that knows
	// nothing about skills.
	a.SetStart(start)
	if want := systemPrompt(cwd, sh, start, nil); a.system != want || strings.Contains(a.system, "available_skills") {
		t.Fatalf("no skills changed the prompt:\n%s", a.system)
	}
	base := a.system

	write(t, filepath.Join(dir, "skills", "pdf", "SKILL.md"), "---\nname: pdf\ndescription: Work with PDFs\n---\nsteps")
	write(t, filepath.Join(repo, ".agents", "skills", "hidden", "SKILL.md"), "---\nname: hidden\ndescription: Only by command\ndisable-model-invocation: true\n---\nx")
	a.SetStart(start)
	loc := filepath.Join(dir, "skills", "pdf", "SKILL.md")
	if !strings.HasPrefix(a.system, base) || !strings.Contains(a.system, "<location>"+loc+"</location>") || strings.Contains(a.system, "Only by command") {
		t.Fatalf("prompt:\n%s", a.system)
	}
	if got, _ := a.Skills(); len(got) != 2 { // the hidden one stays available for /skill:
		t.Fatalf("snapshot: %+v", got)
	}

	// A skill added later does not change the running session's prompt.
	before := a.system
	write(t, filepath.Join(dir, "skills", "late", "SKILL.md"), "---\nname: late\ndescription: Late\n---\n")
	if a.system != before {
		t.Fatal("prompt changed mid-session")
	}
}
