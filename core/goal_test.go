package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/provider"
)

func step(in, cached, out int) agent.StepEnd {
	return agent.StepEnd{Usage: provider.Usage{PromptTokens: in, CachedTokens: cached, CompletionTokens: out}}
}

func TestGoalDriverBudget(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	g, _ := goal.New("ship it", 100)
	var steers []string
	var changes []goal.Status
	snaps := 0
	d := GoalDriver{Session: "s", Goal: g,
		Steer:    func(s string) { steers = append(steers, s) },
		Changed:  func(g *goal.Goal) { changes = append(changes, g.Status) },
		Snapshot: func(*goal.Goal) { snaps++ },
	}
	d.BeginTurn()
	d.Event(agent.ToolStart{})
	d.Event(step(60, 20, 10)) // 50 new tokens
	if len(steers) != 0 || g.TokensUsed != 50 {
		t.Fatalf("under budget: %v %d", steers, g.TokensUsed)
	}
	d.Event(step(80, 60, 40)) // 60 more: over
	d.Event(step(80, 60, 40)) // no longer active: not counted, no second message
	if len(steers) != 1 || !strings.Contains(steers[0], "budget") || g.Status != goal.BudgetLimited || g.TokensUsed != 110 {
		t.Fatalf("budget: %v %s %d", steers, g.Status, g.TokensUsed)
	}
	if d.EndTurn(nil) || g.Turns != 1 || snaps != 1 {
		t.Fatalf("turn end: active, %d turns, %d snapshots", g.Turns, snaps)
	}
	if len(changes) != 1 || changes[0] != goal.BudgetLimited {
		t.Fatalf("changes %v", changes)
	}
}

func TestGoalDriverStops(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	g, _ := goal.New("ship it", 0)
	d := GoalDriver{Session: "s", Goal: g}
	d.BeginTurn()
	if d.EndTurn(context.Canceled) || g.Status != goal.Paused || g.Note != "interrupted" {
		t.Fatalf("an interrupt pauses: %s %q", g.Status, g.Note)
	}
	g.Status = goal.Active
	for i := 0; i < 3; i++ {
		d.BeginTurn()
		d.EndTurn(errors.New("boom"))
	}
	if g.Status != goal.Blocked || !strings.Contains(g.Note, "boom") {
		t.Fatalf("failures block: %s %q", g.Status, g.Note)
	}

	// The model's report comes from the file; nothing else in it counts.
	g.Status, g.Note, g.FailStreak = goal.Active, "", 0
	d.Set(g)
	file, _ := goal.Load("s")
	file.Status, file.Note, file.Objective = goal.Complete, "tests pass", "something else"
	_ = goal.Save("s", file)
	if _, ok := d.Next(); ok || g.Status != goal.Complete || g.Note != "tests pass" || g.Objective != "ship it" {
		t.Fatalf("adopt: %s %q %q", g.Status, g.Note, g.Objective)
	}
}

func TestGoalDriverRun(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	var inputs []string
	turn := func(_ context.Context, input string, emit func(any)) error {
		inputs = append(inputs, input)
		emit(agent.ToolStart{})
		emit(step(10, 0, 1))
		if len(inputs) == 3 { // the model reports from its shell
			f, _ := goal.Load("s")
			f.Status, f.Note = goal.Complete, "done"
			_ = goal.Save("s", f)
		}
		return nil
	}
	seen, between := 0, 0
	emit := func(any) { seen++ }

	// Without a goal: one turn.
	d := GoalDriver{Session: "s"}
	if err := d.Run(context.Background(), "hi", turn, emit, nil); err != nil || len(inputs) != 1 {
		t.Fatalf("no goal: %v %v", err, inputs)
	}

	inputs = nil
	g, _ := goal.New("ship it", 0)
	d.Goal = g
	_ = goal.Save("s", g)
	if err := d.Run(context.Background(), "start", turn, emit, func() { between++ }); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 || inputs[0] != "start" || !strings.HasPrefix(inputs[1], goal.Prefix) || between != 2 {
		t.Fatalf("inputs %q, between %d", inputs, between)
	}
	if g.Status != goal.Complete || g.Turns != 3 || g.TokensUsed != 33 || seen != 8 {
		t.Fatalf("goal %+v, %d events", g, seen)
	}
}
