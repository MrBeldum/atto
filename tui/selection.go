package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// A selection covers rendered body lines, in screen columns of the padded
// line (so it lines up with what the pointer touched). Positions are body
// line indexes, not screen rows, so a selection stays on its text while
// the view scrolls and while output is appended below.

// textPos is a cell of the transcript: a body line and a screen column.
type textPos struct{ line, col int }

func (p textPos) less(q textPos) bool {
	return p.line < q.line || p.line == q.line && p.col < q.col
}

// endCol stands for "the end of the line".
const endCol = 1 << 30

type selUnit int

const (
	unitChar selUnit = iota
	unitWord         // after a double click, a drag extends by words
	unitLine         // after a triple click, by lines
)

type selection struct {
	active bool    // lo..hi is selected and highlighted
	lo, hi textPos // inclusive, lo <= hi
	// aLo..aHi is what the drag started on: a cell, a word or a line.
	aLo, aHi textPos
	unit     selUnit
	headHi   bool // the moving end is hi (else lo)
}

// extendTo moves the selection's free end to the unit at p; the unit the
// drag started on stays selected whichever way it goes.
func (t *TUI) extendTo(p textPos) {
	lo, hi := p, p
	switch t.sel.unit {
	case unitWord:
		lo, hi = t.wordSpan(p)
	case unitLine:
		lo, hi = t.lineSpan(p)
	}
	if lo.less(t.sel.aLo) {
		t.sel.lo, t.sel.hi, t.sel.headHi = lo, t.sel.aHi, false
	} else {
		t.sel.lo, t.sel.hi, t.sel.headHi = t.sel.aLo, hi, true
	}
	t.sel.active = true
}

// moveHead extends the selection by a cell or a line from the keyboard,
// keeping the moving end in view.
func (t *TUI) moveHead(key string) {
	head, anchor := t.sel.lo, t.sel.hi
	if t.sel.headHi {
		head, anchor = t.sel.hi, t.sel.lo
	}
	n := len(t.lastBody)
	if n == 0 {
		return
	}
	head.line = min(head.line, n-1)
	lt := parseLine(t.lastBody[head.line])
	head.col = min(head.col, max(0, lt.width-1))
	switch key {
	case "shift+left":
		if i := lt.cellAt(head.col); i > 0 {
			head.col = lt.cols[i-1]
		} else if head.line > 0 {
			head.line--
			head.col = max(0, parseLine(t.lastBody[head.line]).width-1)
		}
	case "shift+right":
		if i := lt.cellAt(head.col); i >= 0 && i+1 < len(lt.cells) {
			head.col = lt.cols[i+1]
		} else if head.line+1 < n {
			head.line++
			head.col = 0
		}
	case "shift+up":
		head.line = max(0, head.line-1)
	case "shift+down":
		head.line = min(n-1, head.line+1)
	}
	t.sel.aLo, t.sel.aHi, t.sel.unit = anchor, anchor, unitChar
	if head.less(anchor) {
		t.sel.lo, t.sel.hi, t.sel.headHi = head, anchor, false
	} else {
		t.sel.lo, t.sel.hi, t.sel.headHi = anchor, head, true
	}
	switch last := t.viewStart + t.viewRows - 1; {
	case head.line < t.viewStart:
		t.ScrollBy(t.viewStart - head.line)
	case head.line > last:
		t.ScrollBy(-(head.line - last))
	}
}

// lineText is a rendered line split into cells, soft-wrap mark removed.
type lineText struct {
	cells []cell
	cols  []int // first column of each cell
	width int
	// cont is the soft-wrap mark the line starts its text with: 0 for a
	// line of its own, ' ' when it continues the previous line after a
	// space, 'j' when it continues a split word. Its text starts at contCol.
	cont    byte
	contCol int
}

func parseLine(raw string) lineText {
	var lt lineText
	if i := strings.Index(raw, wrapMark); i >= 0 {
		lt.contCol = VisibleWidth(raw[:i])
		lt.cont = ' '
		if strings.HasPrefix(raw[i:], wrapJoin) {
			lt.cont = 'j'
		}
		raw = StripWrapMarks(raw)
	}
	lt.cells, _ = cells(raw)
	lt.cols = make([]int, len(lt.cells))
	for i, c := range lt.cells {
		lt.cols[i] = lt.width
		lt.width += c.width
	}
	return lt
}

// cellAt is the index of the cell covering col: the left half of a wide
// character and its right half are the same cell. -1 past the end.
func (lt lineText) cellAt(col int) int {
	for i, c := range lt.cells {
		if col < lt.cols[i]+max(1, c.width) {
			return i
		}
	}
	return -1
}

// text joins the cells that overlap columns from..to (inclusive). A wide
// character is taken whole even if only one of its halves is in range.
func (lt lineText) text(from, to int) string {
	var b strings.Builder
	for i, c := range lt.cells {
		if lt.cols[i]+max(1, c.width)-1 >= from && lt.cols[i] <= to {
			b.WriteString(c.text)
		}
	}
	return b.String()
}

func (t *TUI) line(i int) lineText {
	if i < 0 || i >= len(t.lastBody) {
		return lineText{}
	}
	return parseLine(t.lastBody[i])
}

// span is the part of body line i the selection covers, as columns
// (inclusive). The left margin and, on a continuation line, the prefix
// before the continued text are left out: they are not part of the text.
func (t *TUI) span(i int, lt lineText) (from, to int, ok bool) {
	s := t.sel
	if !s.active || i < s.lo.line || i > s.hi.line {
		return 0, 0, false
	}
	from, to = 0, endCol
	if i == s.lo.line {
		from = s.lo.col
	}
	if i == s.hi.line {
		to = s.hi.col
	}
	if i > s.lo.line && lt.cont != 0 {
		from = max(from, lt.contCol)
	}
	return max(from, t.PaddingX), to, true
}

// SelectedText is the text of the selection: escapes stripped, trailing
// spaces dropped, soft-wrapped lines joined back into the line they were,
// and indentation shared by every line removed.
func (t *TUI) SelectedText() string {
	if !t.sel.active {
		return ""
	}
	var out []string
	var cur string
	for i := t.sel.lo.line; i <= t.sel.hi.line && i < len(t.lastBody); i++ {
		lt := t.line(i)
		from, to, _ := t.span(i, lt)
		piece := lt.text(from, to)
		switch {
		case i == t.sel.lo.line:
			cur = piece
		case lt.cont == ' ':
			cur = strings.TrimRight(cur, " ") + " " + piece
		case lt.cont == 'j':
			cur = strings.TrimRight(cur, " ") + piece
		default:
			out = append(out, strings.TrimRight(cur, " "))
			cur = piece
		}
	}
	out = append(out, strings.TrimRight(cur, " "))

	// Dedent: the first line counts only if the selection took its
	// indentation too.
	lt := t.line(t.sel.lo.line)
	first := t.sel.lo.col <= t.PaddingX+indentOf(lt.text(t.PaddingX, endCol))
	common := -1
	for k, l := range out {
		if l == "" || (k == 0 && !first) {
			continue
		}
		if n := indentOf(l); common < 0 || n < common {
			common = n
		}
	}
	for k, l := range out {
		if common > 0 && l != "" && (k > 0 || first) {
			out[k] = l[common:]
		}
	}
	return strings.Join(out, "\n")
}

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

// highlight draws the selection in reverse video over rows, the visible
// body lines starting at body line start.
func (t *TUI) highlight(rows []string, start int) {
	if !t.sel.active {
		return
	}
	for k := range rows {
		i := start + k
		if i < t.sel.lo.line || i > t.sel.hi.line {
			continue
		}
		from, to, _ := t.span(i, t.line(i))
		rows[k] = reverseCells(rows[k], from, to)
	}
}

// reverseCells puts columns from..to of a styled line in reverse video.
// The line's own escapes may reset attributes, so reverse is set again
// after each of them.
func reverseCells(s string, from, to int) string {
	cs, trailing := cells(s)
	var b strings.Builder
	col, on := 0, false
	for _, c := range cs {
		in := col+max(1, c.width)-1 >= from && col <= to
		b.WriteString(c.esc)
		switch {
		case in && (!on || c.esc != ""):
			b.WriteString("\x1b[7m")
			on = true
		case !in && on:
			b.WriteString("\x1b[27m")
			on = false
		}
		b.WriteString(c.text)
		col += c.width
	}
	if on {
		b.WriteString("\x1b[27m")
	}
	b.WriteString(trailing)
	return b.String()
}

// isWordRune reports whether r belongs to a word for double-click
// selection. Path and URL characters count, so a file path or a URL is one
// word; brackets, quotes, commas and drawing characters end it.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) ||
		strings.ContainsRune("_-./\\~:@%+=#?&$*!^", r)
}

func isWordCell(c cell) bool {
	r, _ := utf8.DecodeRuneInString(c.text)
	return c.text != "" && isWordRune(r)
}

// wordSpan is the word under p. A word split by a soft wrap (a long path
// or URL) continues on the next line. Trailing sentence punctuation is
// left out, so "see /tmp/x." selects /tmp/x. Off a word it is the cell.
func (t *TUI) wordSpan(p textPos) (lo, hi textPos) {
	lt := t.line(p.line)
	i := lt.cellAt(p.col)
	if i < 0 || !isWordCell(lt.cells[i]) {
		return p, p
	}
	// Back to the start, across split-word wraps.
	line, j := p.line, i
	for {
		for j > 0 && isWordCell(lt.cells[j-1]) && (lt.cont == 0 || lt.cols[j-1] >= lt.contCol) {
			j--
		}
		if lt.cont != 'j' || lt.cols[j] != lt.contCol || line == 0 {
			break
		}
		prev := t.line(line - 1)
		if len(prev.cells) == 0 || !isWordCell(prev.cells[len(prev.cells)-1]) {
			break
		}
		line, lt, j = line-1, prev, len(prev.cells)-1
	}
	lo = textPos{line, lt.cols[j]}

	// Forward to the end.
	line, lt, j = p.line, t.line(p.line), i
	for {
		for j+1 < len(lt.cells) && isWordCell(lt.cells[j+1]) {
			j++
		}
		next := t.line(line + 1)
		if j+1 < len(lt.cells) || next.cont != 'j' {
			break
		}
		k := next.cellAt(next.contCol)
		if k < 0 || !isWordCell(next.cells[k]) {
			break
		}
		line, lt, j = line+1, next, k
	}
	// Trailing punctuation, unless that is all there is.
	for (lo.line < line || lt.cols[j] > lo.col) && strings.ContainsAny(lt.cells[j].text, ".,:;!?") {
		if j == 0 || (lt.cont != 0 && lt.cols[j] <= lt.contCol) {
			break
		}
		j--
	}
	return lo, textPos{line, lt.cols[j] + max(1, lt.cells[j].width) - 1}
}

// lineSpan is the line under p, from its first non-blank cell to its end;
// a soft-wrapped line is taken whole.
func (t *TUI) lineSpan(p textPos) (lo, hi textPos) {
	first, last := p.line, p.line
	for first > 0 && t.line(first).cont != 0 {
		first--
	}
	for last+1 < len(t.lastBody) && t.line(last+1).cont != 0 {
		last++
	}
	lt := t.line(first)
	col := 0
	for i, c := range lt.cells {
		if lt.cols[i] >= t.PaddingX && c.text != " " {
			col = lt.cols[i]
			break
		}
	}
	return textPos{first, col}, textPos{last, endCol}
}
