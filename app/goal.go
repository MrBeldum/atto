package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
)

// goalState is the TUI's goal. It lives in memory; the goal file is how
// the model reports back (atto goal complete|blocked), and only those
// reports are taken from it (goal.Adopt), so editing the file cannot
// rewrite the objective or the budget.
type goalState struct {
	g          *goal.Goal
	turnTools  int  // tool calls in the current turn
	budgetSent bool // budget message already steered into this turn
}

// pollGoal picks up a report the model wrote with atto goal complete|blocked.
func (a *App) pollGoal() {
	g := a.goal.g
	if g == nil {
		return
	}
	file, err := goal.Load(a.sess.ID)
	if err != nil {
		a.errorNotice(err)
		return
	}
	if g.Adopt(file) {
		a.saveGoal(g)
		a.announceGoal(g)
	}
}

// saveGoal writes the goal file and records a snapshot in the session.
func (a *App) saveGoal(g *goal.Goal) {
	if g == nil {
		_ = goal.Clear(a.sess.ID)
		a.sess.Append(session.Entry{Type: session.TypeGoal, Goal: json.RawMessage("null")})
	} else {
		if err := goal.Save(a.sess.ID, g); err != nil {
			a.errorNotice(err)
		}
		raw, _ := json.Marshal(g)
		a.sess.Append(session.Entry{Type: session.TypeGoal, Goal: raw})
	}
	a.goal.g = g
}

// announce shows a goal status change in the transcript.
func (a *App) announceGoal(g *goal.Goal) {
	var title string
	switch g.Status {
	case goal.Complete:
		title = "◎ Goal achieved"
	case goal.Blocked:
		title = "◎ Goal blocked (/goal resume to retry)"
	case goal.BudgetLimited:
		title = "◎ Goal budget used (/goal budget <n> to extend)"
	case goal.Paused:
		title = "◎ Goal paused (/goal resume)"
	default:
		return
	}
	if g.Note != "" {
		title += ": " + g.Note
	}
	a.add(&eventBlock{title: title + tui.Dim(" · "+g.Usage())})
}

// goalStep accounts a model call against an active goal and, when the
// budget runs out, tells the model to wrap up (once, mid-turn as in codex).
func (a *App) goalStep(input, cached, output int) {
	a.pollGoal()
	g := a.goal.g
	if g == nil || g.Status != goal.Active {
		return
	}
	exhausted := g.Account(input, cached, output)
	_ = goal.Save(a.sess.ID, g) // the file follows memory; edits to it are dropped
	if exhausted && !a.goal.budgetSent {
		a.goal.budgetSent = true
		a.agent.Steer(g.BudgetMessage())
		a.announceGoal(g)
	}
}

// goalTurnEnded applies stop conditions after a turn. Returns true if the
// goal is still active.
func (a *App) goalTurnEnded(err error) bool {
	a.pollGoal()
	g := a.goal.g
	if g == nil {
		return false
	}
	wasActive := g.Status == goal.Active
	var failed error
	switch {
	case errors.Is(err, context.Canceled):
		if g.Status == goal.Active {
			g.Status, g.Note = goal.Paused, "interrupted"
		}
	case err != nil:
		failed = err
	}
	g.TurnEnded(time.Since(a.runStart), failed, a.goal.turnTools)
	a.saveGoal(g)
	if wasActive && g.Status != goal.Active {
		a.announceGoal(g)
	}
	return g.Status == goal.Active
}

// continueGoal starts the next goal turn when nothing else is waiting:
// user input (queued, pending events, an open picker) always goes first.
func (a *App) continueGoal() {
	if a.busy || a.modal != nil || a.queuePaused || len(a.queued) > 0 || len(a.pendingEvents) > 0 {
		return
	}
	a.pollGoal()
	g := a.goal.g
	if g == nil || g.Status != goal.Active {
		return
	}
	a.add(&eventBlock{title: fmt.Sprintf("◎ Continuing goal · turn %d · %s", g.Turns+1, g.Usage())})
	text := g.Continuation()
	a.runKind = "turn"
	a.goal.turnTools, a.goal.budgetSent = 0, false
	a.recordSettings()
	a.start("Working on goal", func(ctx context.Context, emit func(any)) error {
		return a.agent.Run(ctx, text, emit)
	})
}

// cmdGoal: /goal <objective> | /goal [show] | pause | resume | clear |
// budget <n> | edit.
func (a *App) cmdGoal(arg string) {
	sub, rest, _ := strings.Cut(strings.TrimSpace(arg), " ")
	a.pollGoal()
	g := a.goal.g
	switch sub {
	case "", "show":
		if g == nil {
			a.notice("No goal. Set one with /goal <objective>; atto keeps working on it until it is done, blocked or out of budget.")
			return
		}
		lines := []string{
			tui.Bold("Goal") + tui.Dim(" · "+string(g.Status)+" · "+g.Usage()+fmt.Sprintf(" · %d turns", g.Turns)),
		}
		for _, l := range strings.Split(g.Objective, "\n") {
			lines = append(lines, "  "+l)
		}
		if g.Note != "" {
			lines = append(lines, tui.Dim("note: "+g.Note))
		}
		lines = append(lines, tui.Dim("/goal pause · resume · clear · budget <n> · edit"))
		a.add(&contextBlock{lines: lines})
	case "pause":
		if g == nil || g.Status != goal.Active {
			a.notice("No active goal.")
			return
		}
		g.Status, g.Note = goal.Paused, "paused by the user"
		a.saveGoal(g)
		a.notice("Goal paused. The current turn finishes; no new goal turns start. /goal resume to continue.")
	case "resume":
		if g == nil || g.Status == goal.Active {
			a.notice("Nothing to resume.")
			return
		}
		if g.Budget > 0 && g.TokensUsed >= g.Budget {
			a.notice("The budget is used up; raise it first with /goal budget <n>.")
			return
		}
		g.Status, g.Note, g.FailStreak, g.IdleStreak = goal.Active, "", 0, 0
		a.saveGoal(g)
		a.notice("Goal resumed.")
		a.continueGoal()
	case "clear":
		if g == nil {
			a.notice("No goal.")
			return
		}
		a.saveGoal(nil)
		a.notice("Goal cleared.")
	case "budget":
		if g == nil {
			a.notice("No goal.")
			return
		}
		b, err := goal.ParseBudget(rest)
		if err != nil {
			a.errorNotice(err)
			return
		}
		g.Budget = b
		if g.Status == goal.BudgetLimited && g.TokensUsed < b {
			g.Status, g.Note = goal.Paused, "budget raised"
		}
		a.saveGoal(g)
		a.notice("Goal budget set to %s (%s used).", goal.Tokens(b), goal.Tokens(g.TokensUsed))
	case "edit":
		if g == nil {
			a.notice("No goal.")
			return
		}
		a.editor.SetText("/goal " + g.Objective)
	default:
		ng, err := goal.New(strings.TrimSpace(arg), 0)
		if err != nil {
			a.errorNotice(err)
			return
		}
		if g != nil && g.Status != goal.Complete {
			a.notice("Replaced the previous goal.")
		}
		a.saveGoal(ng)
		a.add(&eventBlock{title: "◎ Goal set: " + tui.FirstLine(ng.Objective)})
		a.continueGoal() // starts now if idle, else after the current turn
	}
}

// restoreGoal brings a resumed session's goal back from its last snapshot.
// An active goal comes back paused, so resuming never starts work by itself.
func (a *App) restoreGoal(entries []session.Entry) {
	var last json.RawMessage
	for _, e := range entries {
		if e.Type == session.TypeGoal {
			last = e.Goal
		}
	}
	a.goal = goalState{}
	if len(last) == 0 || string(last) == "null" {
		_ = goal.Clear(a.sess.ID)
		return
	}
	var g goal.Goal
	if json.Unmarshal(last, &g) != nil {
		return
	}
	if g.Status == goal.Active {
		g.Status, g.Note = goal.Paused, "session resumed"
		a.notice("This session has a goal; it is paused. /goal resume to continue.")
	}
	_ = goal.Save(a.sess.ID, &g)
	a.goal.g = &g
}

// goalPill is the status line indicator.
func (a *App) goalPill() string {
	g := a.goal.g
	if g == nil {
		return ""
	}
	switch g.Status {
	case goal.Active:
		return tui.FG(6, "◎ goal "+g.Usage())
	case goal.Paused:
		return tui.FG(3, "◎ goal paused (/goal resume)")
	case goal.Blocked:
		return tui.FG(1, "◎ goal blocked (/goal)")
	case goal.BudgetLimited:
		return tui.FG(3, "◎ goal budget used (/goal)")
	case goal.Complete:
		return tui.FG(2, "◎ goal achieved")
	}
	return ""
}
