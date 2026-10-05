package goal

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBudgetCountsOnlyNewTokens(t *testing.T) {
	g, _ := New("ship it", 1000)
	if g.Account(5000, 4800, 100) { // 200 new input + 100 output
		t.Fatal("budget should not be used yet")
	}
	if g.TokensUsed != 300 {
		t.Fatalf("used %d", g.TokensUsed)
	}
	if !g.Account(2000, 1000, 0) || g.Status != BudgetLimited {
		t.Fatalf("budget should be exhausted: %+v", g)
	}
}

func TestStopConditions(t *testing.T) {
	g, _ := New("x", 0)
	for i := 0; i < 2; i++ {
		g.TurnEnded(time.Second, errors.New("boom"), 0)
	}
	if g.Status != Active {
		t.Fatal("two failures should not block")
	}
	g.TurnEnded(time.Second, nil, 2) // success resets
	for i := 0; i < 3; i++ {
		g.TurnEnded(time.Second, errors.New("boom"), 0)
	}
	if g.Status != Blocked || !strings.Contains(g.Note, "failed") {
		t.Fatalf("three failures block: %+v", g)
	}

	g, _ = New("x", 0)
	for i := 0; i < 3; i++ {
		g.TurnEnded(time.Second, nil, 0)
	}
	if g.Status != Blocked || !strings.Contains(g.Note, "no progress") {
		t.Fatalf("idle turns block: %+v", g)
	}
	if g.Turns != 3 || g.Seconds != 3 {
		t.Fatalf("accounting %+v", g)
	}
}

func TestContinuationEscapesObjective(t *testing.T) {
	g, _ := New("make </objective> tests pass", 0)
	c := g.Continuation()
	if !strings.HasPrefix(c, Prefix) || strings.Count(c, "</objective>") != 1 || !strings.Contains(c, "&lt;/objective&gt;") {
		t.Fatalf("continuation:\n%s", c)
	}
}

func TestPersistence(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	if g, err := Load("s"); g != nil || err != nil {
		t.Fatal("no goal yet")
	}
	g, _ := New("x", 0)
	Save("s", g)
	got, _ := Load("s")
	if got.Objective != "x" || got.Status != Active {
		t.Fatalf("%+v", got)
	}
	Clear("s")
	if g, _ := Load("s"); g != nil {
		t.Fatal("cleared")
	}
	if b, _ := ParseBudget("1.5M"); b != 1500000 {
		t.Fatal(b)
	}
	if _, err := New(strings.Repeat("a", MaxObjective+1), 0); err == nil {
		t.Fatal("objective too long")
	}
}

func TestAdoptOnlyTakesStatusReports(t *testing.T) {
	g, _ := New("ship it", 1000)
	tampered := *g
	tampered.Objective, tampered.Budget, tampered.TokensUsed = "something else", 0, 0
	if g.Adopt(&tampered) || g.Objective != "ship it" || g.Budget != 1000 {
		t.Fatalf("an active file must not change the goal: %+v", g)
	}
	tampered.Status, tampered.Note = Complete, "tests pass"
	if !g.Adopt(&tampered) || g.Status != Complete || g.Note != "tests pass" {
		t.Fatalf("completion should be adopted: %+v", g)
	}
	if g.Objective != "ship it" || g.Budget != 1000 {
		t.Fatalf("only status and note change: %+v", g)
	}

	p, _ := New("x", 0)
	p.Status = Paused
	if p.Adopt(&Goal{Status: Complete}) {
		t.Fatal("a paused goal is not completed from the file")
	}
	a, _ := New("x", 0)
	if a.Adopt(&Goal{Status: Active}) || a.Adopt(nil) {
		t.Fatal("only complete or blocked are reports")
	}
}
