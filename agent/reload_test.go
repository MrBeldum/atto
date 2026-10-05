package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReloadRebuildsPromptOnlyOnChange(t *testing.T) {
	repo, cwd, dir := project(t)
	write(t, filepath.Join(repo, ".git", "HEAD"), "x")
	write(t, filepath.Join(repo, "AGENTS.md"), "first rules")
	a := New(newTestAgent("http://x").model, "", cwd)
	a.SetStart(time.Date(2026, 1, 2, 3, 0, 0, 0, time.Local))
	before := a.SystemPrompt()
	if a.Reload() || a.SystemPrompt() != before {
		t.Fatal("nothing changed: the prompt must stay")
	}

	write(t, filepath.Join(repo, "AGENTS.md"), "second rules")
	write(t, filepath.Join(dir, "skills", "late", "SKILL.md"), "---\nname: late\ndescription: Late\n---\n")
	if !a.Reload() {
		t.Fatal("changed files must rebuild the prompt")
	}
	p := a.SystemPrompt()
	if !strings.Contains(p, "second rules") || strings.Contains(p, "first rules") || !strings.Contains(p, "<name>late</name>") ||
		!strings.Contains(p, "Session started: 2026-01-02") {
		t.Fatalf("prompt:\n%s", p)
	}
	if sk, _ := a.Skills(); len(sk) != 1 || sk[0].Name != "late" {
		t.Fatalf("skills %+v", sk)
	}
	src := a.Sources()
	if len(src.Instructions) != 1 || src.Instructions[0].Path != filepath.Join(repo, "AGENTS.md") || src.InstructionBytes == 0 || src.SkillBytes == 0 {
		t.Fatalf("sources %+v", src)
	}
}

func TestInstructionsSkippedAndKept(t *testing.T) {
	repo, cwd, _ := project(t)
	write(t, filepath.Join(repo, ".git", "HEAD"), "x")
	write(t, filepath.Join(repo, "AGENTS.override.md"), "  ")
	write(t, filepath.Join(repo, "AGENTS.md"), "root")
	write(t, filepath.Join(repo, "CLAUDE.md"), "claude")
	write(t, filepath.Join(cwd, "AGENTS.md"), strings.Repeat("é", 20*1024)) // 40 KiB, two bytes a rune
	files, skipped := scanInstructions(cwd)
	if len(skipped) != 2 || skipped[0].Reason != "empty" || skipped[1].Reason != "shadowed by AGENTS.md" {
		t.Fatalf("skipped %+v", skipped)
	}
	in := describeInstructions(files)
	if len(in) != 2 || in[0].Kept != 4 || in[1].Kept >= in[1].Bytes || in[1].Kept%2 != 0 || in[0].Kept+in[1].Kept > maxInstructionBytes {
		t.Fatalf("kept %+v", in)
	}
}

// A function queued with AtBoundary runs after the step's tool calls, on
// the turn's goroutine, and what it returns reaches the model like a steer
// in the next request.
func TestAtBoundaryRunsBetweenSteps(t *testing.T) {
	srv, seen := fakeServer(t, toolCall("echo hi"), text("done"))
	a := newTestAgent(srv.URL)
	var ran []string
	err := a.Run(context.Background(), "go", func(ev any) {
		if _, ok := ev.(ToolStart); ok {
			a.AtBoundary(func() string {
				ran = append(ran, "boundary")
				return "[atto event] Reload applied"
			})
		}
		if _, ok := ev.(ToolEnd); ok {
			ran = append(ran, "tool end")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "tool end,boundary" {
		t.Fatalf("order %v", ran)
	}
	reqs := seen()
	last := reqs[1][len(reqs[1])-1]
	if last["role"] != "user" || last["content"] != "[atto event] Reload applied" {
		t.Fatalf("second request ends with %v", last)
	}
	if len(a.TakeBoundary()) != 0 {
		t.Fatal("ran functions must not stay queued")
	}

	// Queued when the model stops, the result keeps the turn going.
	srv2, seen2 := fakeServer(t, text("first"), text("second"))
	b := newTestAgent(srv2.URL)
	b.AtBoundary(func() string { return "report" })
	if err := b.Run(context.Background(), "hi", func(any) {}); err != nil || len(seen2()) != 2 {
		t.Fatalf("the report starts another step: %v %d", err, len(seen2()))
	}

	// One no boundary reached waits for TakeBoundary.
	c := newTestAgent("http://x")
	c.AtBoundary(func() string { return "" })
	if len(c.TakeBoundary()) != 1 {
		t.Fatal("left for the front end")
	}
}
