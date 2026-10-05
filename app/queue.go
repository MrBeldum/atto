package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/events"

	"github.com/sebastianrcnt/atto/tui"
)

// Pending input follows codex-rs:
//
//   - Enter while a turn runs sends a steer. It is delivered into the running
//     turn after the next tool call (or when the model stops, which continues
//     the turn). Esc interrupts and sends pending steers immediately.
//   - Tab queues a follow-up that starts as a new turn when the current one
//     ends. Shift+Left pulls the last queued message back into the editor.

const previewLineLimit = 3

func (a *App) steer(text string) {
	a.agent.Steer(text)
	a.pendingSteers = append(a.pendingSteers, text)
	events.Wake(a.sess.ID) // `atto sleep` / `atto job wait` return early
}

func (a *App) enqueue(text string) {
	a.queued = append(a.queued, text)
	a.maybeSendNextQueued()
}

// queueFromEditor handles Tab: queue the draft, or submit it when idle.
func (a *App) queueFromEditor() {
	text := a.editor.Commit()
	if text == "" {
		return
	}
	if !a.busy {
		a.submit(text)
		return
	}
	a.enqueue(text)
}

func (a *App) editLastQueued() {
	last := a.queued[len(a.queued)-1]
	a.queued = a.queued[:len(a.queued)-1]
	if cur := a.editor.Text(); strings.TrimSpace(cur) != "" {
		last += "\n" + cur
	}
	a.editor.SetText(last)
}

func (a *App) restoreToEditor(texts []string) {
	text := strings.Join(texts, "\n\n")
	if cur := a.editor.Text(); strings.TrimSpace(cur) != "" {
		text += "\n\n" + cur
	}
	a.editor.SetText(text)
}

// afterRun settles pending input once a turn or compaction finishes.
func (a *App) afterRun(err error) {
	if a.runKind == "turn" {
		a.goalTurnEnded(err)
	}
	if a.runKind == "turn" && err == nil {
		took := "<1s"
		if d := time.Since(a.runStart); d >= time.Second {
			took = fmtDur(d.Truncate(time.Second))
		}
		a.notice("Worked for %s • %s", took, time.Now().Format("3:04 PM"))
	}
	a.runKind = ""
	if id := a.pendingTree; id != "" {
		a.pendingTree = ""
		a.navigateTree(id)
		return
	}
	if p := a.pendingResume; p != "" {
		a.pendingResume = ""
		a.agent.DrainSteers()
		a.pendingSteers, a.sendSteersAfterInterrupt = nil, false
		a.resume(p)
		return
	}
	canceled := errors.Is(err, context.Canceled)
	leftover := a.agent.DrainSteers()
	a.pendingSteers = nil
	sendSteers := a.sendSteersAfterInterrupt
	a.sendSteersAfterInterrupt = false

	if len(leftover) > 0 {
		// Steers that raced with the end of the turn, or that the user asked
		// to send right away with Esc, start the next turn. Otherwise (error,
		// Ctrl+C) they go back into the editor.
		if err == nil || (canceled && sendSteers) {
			a.startTurn(strings.Join(leftover, "\n\n"))
			return
		}
		a.restoreToEditor(leftover)
	}

	if len(a.pendingEvents) > 0 && err == nil {
		a.deliverEvents()
		if a.busy {
			return
		}
	}
	if err != nil && len(a.queued) > 0 {
		a.queuePaused = true
		a.notice("Queued messages paused. Press enter on an empty prompt to resume, or shift+← to edit.")
		return
	}
	a.maybeSendNextQueued()
}

// maybeSendNextQueued starts the next queued follow-up when idle. Queued
// slash commands that don't start a run are executed in order.
func (a *App) maybeSendNextQueued() {
	for !a.busy && a.modal == nil && !a.queuePaused && len(a.queued) > 0 {
		next := a.queued[0]
		a.queued = a.queued[1:]
		if strings.HasPrefix(next, "/") {
			a.runCommand(next)
			continue
		}
		a.startTurn(next)
		return
	}
	a.continueGoal()
}

func previewLines(text string, width int, style func(string) string) []string {
	var out []string
	wrapped := tui.Wrap(text, max(1, width-4))
	for i, l := range wrapped {
		if i == previewLineLimit {
			out = append(out, "    "+tui.Dim(style("…")))
			break
		}
		prefix := "    "
		if i == 0 {
			prefix = tui.Dim("  ↳ ")
		}
		out = append(out, prefix+tui.Dim(style(l)))
	}
	return out
}

func (a *App) renderPending(width int) []string {
	if len(a.pendingSteers) == 0 && len(a.queued) == 0 {
		return nil
	}
	plain := func(s string) string { return s }
	out := []string{""}
	if len(a.pendingSteers) > 0 {
		out = append(out, tui.Truncate(tui.Dim("• ")+"Messages to be submitted after next tool call"+
			tui.Dim(" (press esc to interrupt and send immediately)"), width, "…"))
		for _, s := range a.pendingSteers {
			out = append(out, previewLines(s, width, plain)...)
		}
	}
	if len(a.queued) > 0 {
		if len(a.pendingSteers) > 0 {
			out = append(out, "")
		}
		header := "Queued follow-up inputs"
		if a.queuePaused {
			header += tui.Dim(" (paused — enter on empty prompt to resume)")
		}
		out = append(out, tui.Truncate(tui.Dim("• ")+header, width, "…"))
		for _, s := range a.queued {
			out = append(out, previewLines(s, width, tui.Italic)...)
		}
		out = append(out, "    "+tui.FG(6, "shift+←")+tui.Dim(" edit last queued message"))
	}
	return out
}
