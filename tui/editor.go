package tui

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// Editor is a multi-line prompt input. Enter submits; Alt+Enter or Ctrl+J
// inserts a newline. Up/Down walk submission history when the buffer is a
// single line.
type Editor struct {
	Prompt   string
	OnSubmit func(text string)
	// Rule styles the horizontal rules drawn above and below the input.
	Rule func(string) string

	buf     []rune
	pos     int
	focused bool

	history []string
	histIdx int    // len(history) means "not browsing"
	draft   []rune // buffer saved when history browsing starts
}

func NewEditor(prompt string) *Editor { return &Editor{Prompt: prompt} }

func (e *Editor) SetFocused(f bool) { e.focused = f }
func (e *Editor) Text() string      { return string(e.buf) }

func (e *Editor) SetText(s string) {
	e.buf = []rune(s)
	e.pos = len(e.buf)
}

func (e *Editor) insert(s string) {
	rs := []rune(s)
	e.buf = append(e.buf[:e.pos], append(rs, e.buf[e.pos:]...)...)
	e.pos += len(rs)
}

func (e *Editor) deleteRange(from, to int) {
	if from < 0 {
		from = 0
	}
	if to > len(e.buf) {
		to = len(e.buf)
	}
	if from >= to {
		return
	}
	e.buf = append(e.buf[:from], e.buf[to:]...)
	e.pos = from
}

func (e *Editor) lineStart() int {
	i := e.pos
	for i > 0 && e.buf[i-1] != '\n' {
		i--
	}
	return i
}

func (e *Editor) lineEnd() int {
	i := e.pos
	for i < len(e.buf) && e.buf[i] != '\n' {
		i++
	}
	return i
}

func (e *Editor) wordLeft() int {
	i := e.pos
	for i > 0 && unicode.IsSpace(e.buf[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.buf[i-1]) {
		i--
	}
	return i
}

func (e *Editor) wordRight() int {
	i := e.pos
	for i < len(e.buf) && unicode.IsSpace(e.buf[i]) {
		i++
	}
	for i < len(e.buf) && !unicode.IsSpace(e.buf[i]) {
		i++
	}
	return i
}

// moveLine moves the cursor to the same column on the previous (-1) or next
// (+1) logical line. Returns false if there is no such line.
func (e *Editor) moveLine(dir int) bool {
	start, end := e.lineStart(), e.lineEnd()
	col := e.pos - start
	if dir < 0 {
		if start == 0 {
			return false
		}
		e.pos = start - 1
		ps := e.lineStart()
		e.pos = ps + min(col, e.pos-ps)
		return true
	}
	if end == len(e.buf) {
		return false
	}
	e.pos = end + 1
	ne := e.lineEnd()
	e.pos = min(e.pos+col, ne)
	return true
}

func (e *Editor) browseHistory(dir int) {
	if len(e.history) == 0 {
		return
	}
	if e.histIdx == len(e.history) {
		e.draft = append([]rune(nil), e.buf...)
	}
	e.histIdx = min(max(e.histIdx+dir, 0), len(e.history))
	if e.histIdx == len(e.history) {
		e.buf = append([]rune(nil), e.draft...)
	} else {
		e.buf = []rune(e.history[e.histIdx])
	}
	e.pos = len(e.buf)
}

// Commit records the current text in history, clears the editor and returns
// the trimmed text ("" if blank).
func (e *Editor) Commit() string {
	text := strings.TrimSpace(string(e.buf))
	if text == "" {
		return ""
	}
	e.history = append(e.history, string(e.buf))
	e.histIdx = len(e.history)
	e.buf, e.pos = nil, 0
	return text
}

func (e *Editor) HandleInput(data string) {
	if strings.HasPrefix(data, PastePrefix) {
		p := strings.ReplaceAll(data[len(PastePrefix):], "\r\n", "\n")
		e.insert(strings.ReplaceAll(p, "\r", "\n"))
		return
	}
	switch Key(data) {
	case "enter":
		text := e.Commit()
		if e.OnSubmit != nil {
			e.OnSubmit(text)
		}
	case "alt+enter", "ctrl+j":
		e.insert("\n")
	case "backspace":
		e.deleteRange(e.pos-1, e.pos)
	case "delete", "ctrl+d":
		p := e.pos
		e.deleteRange(p, p+1)
		e.pos = p
	case "left", "ctrl+b":
		e.pos = max(0, e.pos-1)
	case "right", "ctrl+f":
		e.pos = min(len(e.buf), e.pos+1)
	case "home", "ctrl+a":
		e.pos = e.lineStart()
	case "end", "ctrl+e":
		e.pos = e.lineEnd()
	case "word-left":
		e.pos = e.wordLeft()
	case "word-right":
		e.pos = e.wordRight()
	case "ctrl+w", "alt+backspace":
		e.deleteRange(e.wordLeft(), e.pos)
	case "ctrl+u":
		e.deleteRange(e.lineStart(), e.pos)
	case "ctrl+k":
		e.deleteRange(e.pos, e.lineEnd())
	case "up":
		if !e.moveLine(-1) {
			e.browseHistory(-1)
		}
	case "down":
		if !e.moveLine(1) {
			e.browseHistory(1)
		}
	default:
		if Printable(data) {
			e.insert(data)
		}
	}
}

func (e *Editor) Render(width int) []string {
	border := func(s string) string {
		if e.Rule != nil {
			return e.Rule(s)
		}
		return s
	}
	// Horizontal rules above and below, one column of padding before the
	// prompt.
	width = max(width, 4)
	promptW := VisibleWidth(e.Prompt)
	cw := max(1, width-2-promptW)

	var rows []string
	var row strings.Builder
	rowW := 0
	flush := func() {
		rows = append(rows, row.String())
		row.Reset()
		rowW = 0
	}
	for i := 0; i <= len(e.buf); i++ {
		if i == e.pos && e.focused {
			if rowW >= cw {
				flush()
			}
			row.WriteString(CursorMarker)
		}
		if i == len(e.buf) {
			break
		}
		r := e.buf[i]
		if r == '\n' {
			flush()
			continue
		}
		w := uniseg.StringWidth(string(r))
		if rowW+w > cw {
			flush()
		}
		row.WriteRune(r)
		rowW += w
	}
	flush()

	indent := strings.Repeat(" ", promptW)
	rule := border(strings.Repeat("─", width))
	out := make([]string, 0, len(rows)+2)
	out = append(out, rule)
	for i, r := range rows {
		lead := indent
		if i == 0 {
			lead = e.Prompt
		}
		out = append(out, " "+lead+r)
	}
	return append(out, rule)
}
