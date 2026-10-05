package app

import (
	"errors"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/core/transcript"
	"github.com/sebastianrcnt/atto/events"
	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
)

// The transcript builder (core/transcript) turns the agent's events, and
// a resumed session's entries, into items; the TUI turns items into
// blocks. Rendering, expanding and clicking stay with the blocks.

// tr is the App's transcript builder, wired to the blocks.
func (a *App) tr() *transcript.Builder {
	if a.items.Handler.Started == nil {
		a.items.Handler = transcript.Handler{Started: a.itemStarted, Delta: a.itemDelta, Completed: a.itemCompleted}
	}
	return &a.items
}

// replay rebuilds the transcript blocks from session entries: pass the
// active branch (session.Active), not the whole file.
func (a *App) replay(entries []session.Entry) {
	a.replaying = true
	defer func() { a.replaying = false }()
	a.resetItems()
	a.tr().Replay(entries)
}

// resetItems forgets the items of a cleared transcript.
func (a *App) resetItems() {
	a.tr().Reset()
	a.thinking, a.text, a.compact = nil, nil, nil
	clear(a.tools)
}

func (a *App) itemStarted(it *transcript.Item) {
	switch it.Kind {
	case transcript.User:
		if a.steered != nil { // a steer: its messages show as one block
			a.steered = append(a.steered, it.Text)
			return
		}
		a.add(&userBlock{text: it.Text})
	case transcript.Event:
		// Live, events are shown with their titles when delivered.
		if a.replaying {
			for _, t := range eventTitles(it.Text) {
				a.add(&eventBlock{title: t})
			}
		}
	case transcript.Goal:
		// Live, continuing shows the goal's progress, and the budget running
		// out is announced as a status change.
		if a.replaying {
			a.add(&eventBlock{title: goalMessageTitle(it.Text)})
		}
	case transcript.GoalStatus:
		if t := goalStatusTitle(it.GoalState); t != "" {
			a.add(&eventBlock{title: t})
		}
	case transcript.Hook:
		style := tui.Dim
		if it.Blocked {
			style = func(s string) string { return tui.FG(3, s) }
		}
		a.add(&noticeBlock{text: "⚑ " + it.HookEvent + ": " + it.Text, style: style})
	case transcript.Notice:
		a.add(&noticeBlock{text: it.Text, style: tui.Dim})
	case transcript.Reasoning:
		a.thinking = &thinkingBlock{start: time.Now(), expander: expander{d: &a.details}}
		a.add(a.thinking)
	case transcript.Assistant:
		a.text = &textBlock{}
		a.add(a.text)
	case transcript.Tool:
		b := &toolBlock{args: agent.BashArgs{Description: it.Description, Command: it.Command},
			timeout: it.Timeout, start: time.Now(), expander: expander{d: &a.details}}
		if a.tools == nil {
			a.tools = map[string]*toolBlock{}
		}
		a.tools[it.ID] = b
		a.add(b)
	case transcript.Compaction:
		a.compact = &compactBlock{auto: it.Auto, running: true, expander: expander{d: &a.details}}
		a.add(a.compact)
	}
}

func (a *App) itemDelta(it *transcript.Item, d string) {
	switch it.Kind {
	case transcript.Reasoning:
		if a.thinking != nil {
			a.thinking.text.WriteString(d)
		}
	case transcript.Assistant:
		if a.text != nil {
			a.text.text.WriteString(d)
		}
	case transcript.Tool:
		if b := a.tools[it.ID]; b != nil {
			b.append(d)
		}
	case transcript.Compaction:
		if a.compact != nil {
			a.compact.notes.WriteString(d)
		}
	}
}

func (a *App) itemCompleted(it *transcript.Item) {
	switch it.Kind {
	case transcript.Reasoning:
		if t := a.thinking; t != nil {
			t.done, t.dur = true, it.Duration
		}
		a.thinking = nil
	case transcript.Assistant:
		a.text = nil
	case transcript.Tool:
		if b := a.tools[it.ID]; b != nil {
			b.done = true
			if r := it.Result; r != nil {
				b.res = agent.BashResult{ExitCode: r.ExitCode, TimedOut: r.TimedOut, Canceled: r.Canceled, Duration: it.Duration,
					Job: r.Job, Background: r.Background}
				if r.Err != "" {
					b.res.Err = errors.New(r.Err)
				}
			}
			delete(a.tools, it.ID)
		}
	case transcript.Compaction:
		c := a.compact
		a.compact = nil
		if c == nil {
			return
		}
		if it.Status == transcript.Failed { // failed or canceled
			a.ui.Body.Remove(gap{c})
			return
		}
		c.running = false
		c.notes.Reset()
		c.notes.WriteString(it.Text)
		c.before, c.after, c.elapsed = it.TokensBefore, it.TokensAfter, it.Duration
	}
}

// eventTitles are the first lines of the events in a message.
func eventTitles(text string) []string {
	var out []string
	for _, e := range strings.Split(strings.TrimPrefix(text, events.Prefix), "\n\n"+events.Prefix) {
		out = append(out, tui.FirstLine(e))
	}
	return out
}

// goalMessageTitle names a goal message on replay.
func goalMessageTitle(text string) string {
	if strings.Contains(text, "<objective>") {
		return "◎ Continuing goal"
	}
	return "◎ " + tui.FirstLine(strings.TrimPrefix(text, goal.Prefix))
}

// goalStatusTitle announces a goal status; "" for none worth showing.
func goalStatusTitle(g *goal.Goal) string {
	if g == nil {
		return ""
	}
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
		return ""
	}
	if g.Note != "" {
		title += ": " + g.Note
	}
	return title + tui.Dim(" · "+g.Usage())
}

// onEvent handles an event of the running agent: the transcript builder
// makes the blocks; the rest is the footer's state and the goal.
func (a *App) onEvent(ev any) {
	if e, ok := ev.(agent.SteerCommitted); ok {
		n := 0 // the user's own steers, shown as pending until now
		for _, t := range e.Texts {
			if !isEvent(t) && !strings.HasPrefix(t, goal.Prefix) {
				n++
			}
		}
		a.pendingSteers = a.pendingSteers[min(n, len(a.pendingSteers)):]
		a.steered = []string{}
		a.tr().Event(ev)
		if len(a.steered) > 0 {
			a.add(&userBlock{text: strings.Join(a.steered, "\n\n")})
		}
		a.steered = nil
		return
	}
	a.tr().Event(ev)
	a.goal.Event(ev)
	switch e := ev.(type) {
	case agent.ToolStart:
		a.activity = e.Args.Description
	case agent.ToolEnd:
		a.activity = "Thinking"
	case agent.StepEnd:
		a.ctxTokens = e.Context
		a.usage.add(e.Usage)
		a.statusTrigger()
	case agent.CompactStart:
		a.activity = "Compacting context"
	case agent.CompactEnd:
		a.ctxTokens = e.After
		a.activity = "Thinking"
		a.notice("Long threads and repeated compactions can make the model less accurate. Start a new conversation (/clear) when you can.")
	}
}

// announceGoal shows a goal status change in the transcript.
func (a *App) announceGoal(g *goal.Goal) {
	if g == nil || goalStatusTitle(g) == "" {
		return
	}
	c := *g
	a.tr().Add(transcript.Item{Kind: transcript.GoalStatus, GoalState: &c})
}
