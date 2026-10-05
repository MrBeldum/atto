package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/tui"
)

// gap renders a blank line before a block, except at the very top.
type gap struct{ tui.Component }

func (g gap) Render(width int) []string {
	return append([]string{""}, g.Component.Render(width)...)
}

// userBlock shows a submitted prompt on a shaded band, codex-style, with a
// blank shaded row above and below.
type userBlock struct{ text string }

// userBG is the band color: a dark gray that reads on dark themes and
// stays subtle on light ones.
const userBG = 237

func band(content string, width int) string {
	return tui.BG(userBG, content+strings.Repeat(" ", max(0, width-tui.VisibleWidth(content))))
}

func (u *userBlock) Render(width int) []string {
	lines := tui.Wrap(u.text, max(1, width-2))
	out := []string{band("", width)}
	for i, l := range lines {
		lead := "  "
		if i == 0 {
			lead = tui.FG(6, "› ")
		}
		out = append(out, band(lead+l, width))
	}
	return append(out, band("", width))
}

// pinLine renders the one-line pinned form of a prompt.
func (u *userBlock) pinLine(width int) string {
	text := strings.Join(strings.Fields(u.text), " ")
	return band(tui.Truncate(tui.FG(6, "› ")+text, width, "…"), width)
}

// textBlock shows assistant text rendered as markdown, indented under the
// prompt. Output is cached until the text or width changes.
type textBlock struct {
	text strings.Builder

	cacheLen   int
	cacheWidth int
	cache      []string
}

func (t *textBlock) Render(width int) []string {
	if t.cache != nil && t.cacheLen == t.text.Len() && t.cacheWidth == width {
		return t.cache
	}
	lines := tui.Markdown(strings.TrimSpace(t.text.String()), max(1, width-2))
	for i, l := range lines {
		if i == 0 && !startsWithMarker(l) {
			lines[i] = tui.Dim("• ") + l
		} else {
			lines[i] = "  " + l
		}
	}
	t.cache, t.cacheLen, t.cacheWidth = lines, t.text.Len(), width
	return lines
}

const (
	thinkingPreviewLines = 6
	toolPreviewLines     = 3 // codex shows the last 3 output lines
	toolKeepBytes        = 64 * 1024
)

// disclosure renders the expand/collapse affordance shared by collapsible
// blocks: "+ N lines" when collapsed, "− Show less" when expanded.
func disclosure(expanded bool, hidden int, what string) string {
	switch {
	case expanded:
		return tui.Dim("    − Show less (click)")
	case hidden > 0:
		return tui.Dim(fmt.Sprintf("    + %d %s (click or ctrl+t to expand)", hidden, what))
	}
	return ""
}

// details is the global "expand everything" state toggled by ctrl+t. Each
// toggle bumps gen, which resets per-block choices.
type details struct {
	on  bool
	gen int
}

// expander holds a block's expanded state: a click sets a per-block choice
// that overrides the global ctrl+t state until ctrl+t is pressed again.
type expander struct {
	d   *details
	set bool
	val bool
	gen int
}

func (e *expander) expanded() bool {
	if e.set && (e.d == nil || e.gen == e.d.gen) {
		return e.val
	}
	return e.d != nil && e.d.on
}

func (e *expander) toggle() {
	v := !e.expanded()
	e.set, e.val = true, v
	if e.d != nil {
		e.gen = e.d.gen
	}
}

// clickable records, at render time, which lines of a block toggle it: its
// header and its disclosure line, and only when there is something to
// expand or collapse. A click anywhere else in the block (to start a
// selection, say) leaves it alone.
type clickable struct {
	more  bool // something to expand or collapse
	lines int  // lines rendered
	foot  bool // the last line is a disclosure line
}

func (c *clickable) clicks(more bool, out []string, foot bool) []string {
	c.more, c.lines, c.foot = more, len(out), foot
	return out
}

func (c clickable) hit(line int) bool {
	return c.more && (line == 0 || c.foot && line == c.lines-1)
}

// gapped components forward clicks past the leading blank line.
func (g gap) Click(line int) bool {
	if c, ok := g.Component.(tui.Clickable); ok && line > 0 {
		return c.Click(line - 1)
	}
	return false
}

// thinkingBlock streams reasoning dimmed, then collapses to one line that
// expands on click.
type thinkingBlock struct {
	expander
	clickable
	text  strings.Builder
	start time.Time
	dur   time.Duration
	done  bool
}

func (t *thinkingBlock) finish() {
	if !t.done {
		t.done = true
		t.dur = time.Since(t.start)
	}
}

func (t *thinkingBlock) Click(line int) bool {
	if !t.hit(line) {
		return false
	}
	t.toggle()
	return true
}

func (t *thinkingBlock) Render(width int) []string {
	text := strings.TrimSpace(t.text.String())
	has := text != "" // Wrap("") is one empty line
	body := tui.Wrap(text, max(1, width-4))
	style := func(s string) string { return tui.Dim(tui.Italic(s)) }
	expanded := t.expanded()
	if !t.done {
		// While streaming: the last few lines, or everything once clicked.
		head := style("  ∴ Thinking…")
		hidden := 0
		if !expanded && len(body) > thinkingPreviewLines {
			hidden = len(body) - thinkingPreviewLines
			body = body[hidden:]
		}
		if hidden > 0 {
			head += tui.Dim(fmt.Sprintf(" · %d earlier lines · click to expand", hidden))
		} else if expanded {
			head += tui.Dim(" · click to collapse")
		}
		out := []string{tui.Truncate(head, width, "…")}
		for _, l := range body {
			out = append(out, "    "+style(l))
		}
		return t.clicks(hidden > 0 || expanded, out, false)
	}
	head := style(fmt.Sprintf("  ∴ Thought for %s", tui.FormatDuration(t.dur)))
	if !expanded {
		if has {
			head += tui.Dim(" · click to expand")
		}
		return t.clicks(has, []string{tui.Truncate(head, width, "…")}, false)
	}
	out := []string{head}
	for _, l := range body {
		out = append(out, "    "+style(l))
	}
	return t.clicks(has, append(out, disclosure(true, 0, "")), true)
}

// toolBlock shows a bash call: the model's description, the command, the
// last few output lines and the outcome. Click expands the full output.
type toolBlock struct {
	expander
	clickable
	args    agent.BashArgs
	timeout time.Duration
	pending bool // the model is still writing the call; start is when it ran
	start   time.Time
	output  strings.Builder
	total   int // total output bytes seen
	done    bool
	res     agent.BashResult
}

func (b *toolBlock) append(s string) {
	b.total += len(s)
	b.output.WriteString(s)
	if b.output.Len() > 2*toolKeepBytes {
		keep := b.output.String()[b.output.Len()-toolKeepBytes:]
		b.output.Reset()
		b.output.WriteString(keep)
	}
}

func (b *toolBlock) Click(line int) bool {
	if !b.hit(line) {
		return false
	}
	b.toggle()
	return true
}

// displayLines turns raw output into printable lines: escapes stripped,
// carriage-return progress bars collapsed to their final state, trailing
// spaces dropped, and blank lines at either end removed (PowerShell pads
// its tables with them, which would leave the preview empty).
func displayLines(raw string) []string {
	lines := strings.Split(tui.StripEscapes(raw), "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = strings.TrimRight(strings.ReplaceAll(l, "\t", "   "), " ")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// backgroundHintAfter is when a running command's block starts to
// mention ctrl+b: quick commands never show it.
const backgroundHintAfter = 3 * time.Second

func (b *toolBlock) status() (icon, status string) {
	switch {
	case b.pending:
		return tui.FG(3, "●"), "writing…"
	case !b.done:
		ran := time.Since(b.start)
		s := fmt.Sprintf("%s / %s", tui.FormatDuration(ran.Truncate(100*time.Millisecond)), tui.FormatDuration(b.timeout))
		if ran >= backgroundHintAfter && agent.ShellHost {
			s += " · ctrl+b to background"
		}
		return tui.FG(3, "●"), s
	case b.res.Err != nil:
		return tui.FG(1, "✗"), tui.FG(1, b.res.Err.Error())
	case b.res.Job > 0 && b.res.Background == agent.BackgroundRequested:
		return tui.FG(6, "◐"), tui.FG(6, fmt.Sprintf("running in background (job %d)", b.res.Job))
	case b.res.Job > 0:
		return tui.FG(6, "◐"), tui.FG(6, fmt.Sprintf("moved to background (job %d)", b.res.Job)) + tui.Dim(" · after "+tui.FormatDuration(b.res.Duration))
	case b.res.Canceled:
		return tui.FG(1, "✗"), tui.FG(1, "canceled")
	case b.res.TimedOut:
		return tui.FG(1, "✗"), tui.FG(1, "timed out after "+tui.FormatDuration(b.timeout))
	case b.res.ExitCode != 0:
		return tui.FG(1, "✗"), tui.FG(1, fmt.Sprintf("exit %d", b.res.ExitCode)) + tui.Dim(" · "+tui.FormatDuration(b.res.Duration))
	}
	return tui.FG(2, "✓"), tui.FormatDuration(b.res.Duration)
}

func (b *toolBlock) Render(width int) []string {
	icon, status := b.status()
	cmd := strings.TrimSpace(b.args.Command)
	if i := strings.IndexByte(cmd, '\n'); i >= 0 {
		cmd = cmd[:i] + " …"
	}
	// The command goes on its own line: next to the description it got cut
	// off on narrow terminals.
	desc := b.args.Description
	if desc == "" {
		desc = "Preparing command"
	}
	head := icon + " " + tui.Bold(desc) + tui.Dim(" · ") + tui.Dim(status)
	out := []string{tui.Truncate(head, width, tui.Dim("…"))}

	multiLine := strings.Contains(strings.TrimSpace(b.args.Command), "\n")
	lines := displayLines(b.output.String())
	collapsible := len(lines) > toolPreviewLines || multiLine
	expanded := collapsible && b.expanded()

	if expanded && multiLine {
		for i, l := range strings.Split(strings.TrimSpace(b.args.Command), "\n") {
			prefix := "    "
			if i == 0 {
				prefix = "  $ "
			}
			out = append(out, tui.Truncate(tui.Dim(prefix+l), width, tui.Dim("…")))
		}
	} else {
		out = append(out, tui.Truncate(tui.Dim("  $ "+cmd), width, tui.Dim("…")))
	}
	hidden := 0
	if !expanded && len(lines) > toolPreviewLines {
		hidden = len(lines) - toolPreviewLines
		lines = lines[hidden:]
	}
	if expanded && b.total > b.output.Len() {
		out = append(out, tui.Dim(fmt.Sprintf("    (earlier output not kept: %d bytes)", b.total-b.output.Len())))
	}
	if len(lines) == 0 && b.done {
		lines = []string{"(no output)"}
	}
	for i, l := range lines {
		prefix := "    "
		if i == 0 {
			prefix = "  └ "
		}
		out = append(out, tui.Truncate(tui.Dim(prefix+l), width, tui.Dim("…")))
	}
	switch {
	case expanded:
		out = append(out, disclosure(true, 0, ""))
	case hidden > 0:
		out = append(out, disclosure(false, hidden, "lines"))
	case collapsible:
		out = append(out, tui.Dim("    + Show details"))
	}
	// Every collapsible state ends with its disclosure line.
	return b.clicks(collapsible, out, collapsible)
}

// compactBlock reports a compaction; the notes expand on click.
type compactBlock struct {
	expander
	clickable
	auto    bool
	running bool
	notes   strings.Builder
	before  int
	after   int
	elapsed time.Duration
}

func (c *compactBlock) Click(line int) bool {
	if c.running || !c.hit(line) {
		return false
	}
	c.toggle()
	return true
}

func (c *compactBlock) Render(width int) []string {
	kind := "Context compacted"
	if c.auto {
		kind = "Context auto-compacted"
	}
	if c.running {
		out := []string{tui.Dim("  ◇ Compacting context · writing handoff notes…")}
		lines := tui.Wrap(strings.TrimSpace(c.notes.String()), max(1, width-4))
		if len(lines) > thinkingPreviewLines {
			lines = lines[len(lines)-thinkingPreviewLines:]
		}
		for _, l := range lines {
			out = append(out, "    "+tui.Dim(l))
		}
		return c.clicks(false, out, false)
	}
	head := tui.FG(6, "  ◇ ") + kind
	if c.elapsed > 0 {
		head += tui.Dim(" · " + tui.FormatDuration(c.elapsed))
	}
	switch {
	case c.before > 0 && c.after > 0:
		head += tui.Dim(fmt.Sprintf(" · %s → ~%s tokens", tui.FormatTokens(c.before), tui.FormatTokens(c.after)))
	case c.before > 0: // a session saved before the size after was recorded
		head += tui.Dim(fmt.Sprintf(" · %s tokens before", tui.FormatTokens(c.before)))
	}
	if !c.expanded() {
		return c.clicks(true, []string{tui.Truncate(head+tui.Dim(" · click to view notes"), width, "…")}, false)
	}
	out := []string{tui.Truncate(head, width, "…")}
	for _, l := range tui.Markdown(c.notes.String(), max(1, width-4)) {
		out = append(out, "    "+l)
	}
	return c.clicks(true, append(out, disclosure(true, 0, "")), true)
}

// noticeBlock is a one-off status message from atto itself.
type noticeBlock struct {
	text  string
	style func(string) string
}

func (n *noticeBlock) Render(width int) []string {
	lines := tui.Wrap(n.text, max(1, width-2))
	for i, l := range lines {
		lines[i] = "  " + n.style(l)
	}
	return lines
}

// startsWithMarker reports whether a rendered markdown line begins with its
// own marker (list bullet, number, quote or code bar, table border), where
// an extra leading bullet would double up.
func startsWithMarker(line string) bool {
	plain := strings.TrimSpace(tui.StripEscapes(line))
	for _, m := range []string{"•", "◦", "☐", "☑", "│", "╭", "┌", "─"} {
		if strings.HasPrefix(plain, m) {
			return true
		}
	}
	i := strings.IndexFunc(plain, func(r rune) bool { return r < '0' || r > '9' })
	return i > 0 && (plain[i] == '.' || plain[i] == ')')
}
