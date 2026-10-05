package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sebastianrcnt/atto/core"
	"github.com/sebastianrcnt/atto/core/transcript"
	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
)

// The goal is driven by core.GoalDriver, shared with atto -p: the TUI
// feeds it the agent's events (onEvent), ends its turns (afterRun) and
// starts the next goal turn whenever nothing else is waiting.

// resetGoal sets up the goal driver for the current session, without a
// goal: /goal sets one, a resume restores it.
func (a *App) resetGoal() {
	a.goal = core.GoalDriver{
		Session:  a.sess.ID,
		Steer:    func(text string) { a.agent.Steer(text) },
		Snapshot: a.snapshotGoal,
		Changed:  a.announceGoal,
		Error:    a.errorNotice,
	}
}

// snapshotGoal records the goal in the session (null when cleared).
func (a *App) snapshotGoal(g *goal.Goal) {
	raw := json.RawMessage("null")
	if g != nil {
		raw, _ = json.Marshal(g)
	}
	a.sess.Append(session.Entry{Type: session.TypeGoal, Goal: raw})
}

// continueGoal starts the next goal turn when nothing else is waiting:
// user input (queued, pending events, an open picker) always goes first.
func (a *App) continueGoal() {
	if a.busy || a.modal != nil || a.queuePaused || len(a.queued) > 0 || len(a.pendingEvents) > 0 {
		return
	}
	text, ok := a.goal.Next()
	if !ok {
		return
	}
	g := a.goal.Goal
	a.add(&eventBlock{title: fmt.Sprintf("◎ Continuing goal · turn %d · %s", g.Turns+1, g.Usage())})
	a.runKind = "turn"
	a.recordSettings()
	a.tr().Event(transcript.Input{Text: text}) // shown above
	a.start("Working on goal", func(ctx context.Context, emit func(any)) error {
		return a.agent.Run(ctx, text, emit)
	})
}

// cmdGoal: /goal <objective> | /goal [show] | pause | resume | clear |
// budget <n> | edit.
func (a *App) cmdGoal(arg string) {
	sub, rest, _ := strings.Cut(strings.TrimSpace(arg), " ")
	a.goal.Poll()
	g := a.goal.Goal
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
		a.goal.Set(g)
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
		a.goal.Set(g)
		a.notice("Goal resumed.")
		a.continueGoal()
	case "clear":
		if g == nil {
			a.notice("No goal.")
			return
		}
		a.goal.Set(nil)
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
		a.goal.Set(g)
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
		a.goal.Set(ng)
		a.add(&eventBlock{title: "◎ Goal set: " + tui.FirstLine(ng.Objective)})
		a.continueGoal() // starts now if idle, else after the current turn
	}
}

// restoreGoal brings a resumed session's goal back from its last snapshot.
// An active goal comes back paused, so resuming never starts work by itself.
func (a *App) restoreGoal(entries []session.Entry) {
	a.resetGoal()
	if a.goal.Restore(entries) {
		a.notice("This session has a goal; it is paused. /goal resume to continue.")
	}
}

// goalPill is the status line indicator.
func (a *App) goalPill() string {
	g := a.goal.Goal
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
