package app

import (
	"io"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/goal"
)

func TestGoalCommandInsideAgent(t *testing.T) {
	t.Setenv(config.EnvDir, t.TempDir())
	t.Setenv("ATTO_SESSION_ID", "s1")
	t.Setenv(config.EnvAgent, "1")

	if err := RunGoal([]string{"set", "rewrite everything"}, io.Discard); err == nil || !strings.Contains(err.Error(), "only the user") {
		t.Fatalf("set inside an agent: %v", err)
	}
	g, _ := goal.New("ship it", 0)
	_ = goal.Save("s2", g)
	if err := RunGoal([]string{"complete", "-session", "s2", "done"}, io.Discard); err == nil {
		t.Fatal("another session's goal must be off limits")
	}
	_ = goal.Save("s1", g)
	if err := RunGoal([]string{"complete", "tests pass"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := goal.Load("s1"); got.Status != goal.Complete || got.Note != "tests pass" {
		t.Fatalf("report not written: %+v", got)
	}
}
