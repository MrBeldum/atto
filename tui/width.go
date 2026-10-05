package tui

import (
	"strings"

	"github.com/rivo/uniseg"
)

// Reset clears SGR attributes and closes any open OSC 8 hyperlink.
// The renderer appends it to every line so styles never bleed across lines.
const Reset = "\x1b[0m\x1b]8;;\x07"

// escapeLen returns the byte length of the terminal escape sequence starting
// at s[i], or 0 if s[i] does not start one. Handles CSI, OSC, APC/DCS/PM/SOS
// (terminated by BEL or ST) and two-byte ESC sequences.
func escapeLen(s string, i int) int {
	if i >= len(s) || s[i] != 0x1b || i+1 >= len(s) {
		return 0
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			if c := s[j]; c >= 0x40 && c <= 0x7e {
				return j - i + 1
			}
		}
		return len(s) - i
	case ']', '_', 'P', '^', 'X':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j - i + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j - i + 2
			}
		}
		return len(s) - i
	default:
		return 2
	}
}

// StripEscapes removes all terminal escape sequences, keeping visible text.
func StripEscapes(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if n := escapeLen(s, i); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// VisibleWidth returns the number of terminal columns s occupies.
// Escape sequences are zero-width and tabs count as three columns.
func VisibleWidth(s string) int {
	if w, ok := asciiWidth(s); ok {
		return w
	}
	s = StripEscapes(s)
	w := 0
	for _, r := range s {
		if r == '\t' {
			w += 3
		}
	}
	return w + uniseg.StringWidth(strings.ReplaceAll(s, "\t", ""))
}

// asciiWidth is the fast path for printable ASCII mixed with escape sequences,
// which is what most styled lines look like.
func asciiWidth(s string) (int, bool) {
	w := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			n := escapeLen(s, i)
			if n == 0 {
				return 0, false
			}
			i += n
			continue
		case c == '\t':
			w += 3
		case c >= 0x20 && c < 0x7f:
			w++
		default:
			return 0, false
		}
		i++
	}
	return w, true
}

// cell is one grapheme cluster together with the escape sequences that
// precede it.
type cell struct {
	esc   string // escape sequences emitted before the grapheme
	text  string
	width int
}

// cells splits s into graphemes, attaching escape sequences to the following
// grapheme. Trailing escapes are returned separately.
func cells(s string) (out []cell, trailing string) {
	var esc strings.Builder
	for i := 0; i < len(s); {
		if n := escapeLen(s, i); n > 0 {
			esc.WriteString(s[i : i+n])
			i += n
			continue
		}
		// Grapheme run up to the next escape.
		end := strings.IndexByte(s[i:], 0x1b)
		if end < 0 {
			end = len(s)
		} else {
			end += i
		}
		g := uniseg.NewGraphemes(s[i:end])
		for g.Next() {
			t := g.Str()
			w := g.Width()
			if t == "\t" {
				t, w = "   ", 3
			}
			out = append(out, cell{esc: esc.String(), text: t, width: w})
			esc.Reset()
		}
		i = end
	}
	return out, esc.String()
}

// Truncate cuts s to at most width columns, appending tail (e.g. "…") when it
// had to cut. Escape sequences are preserved and a Reset is appended on cut.
func Truncate(s string, width int, tail string) string {
	if VisibleWidth(s) <= width {
		return s
	}
	tw := VisibleWidth(tail)
	if tw > width {
		tail, tw = "", 0
	}
	cs, _ := cells(s)
	var b strings.Builder
	w := 0
	for _, c := range cs {
		if w+c.width > width-tw {
			break
		}
		b.WriteString(c.esc)
		b.WriteString(c.text)
		w += c.width
	}
	b.WriteString(Reset)
	b.WriteString(tail)
	return b.String()
}

// isSGR reports whether esc is a Select Graphic Rendition sequence.
func isSGR(esc string) bool {
	return len(esc) >= 3 && esc[1] == '[' && esc[len(esc)-1] == 'm'
}

func isSGRReset(esc string) bool {
	return esc == "\x1b[m" || esc == "\x1b[0m"
}

// sgrState accumulates active SGR sequences so a wrapped continuation line can
// re-apply the style that was active where the previous line broke.
type sgrState struct{ active string }

func (s *sgrState) feed(esc string) {
	for i := 0; i < len(esc); {
		n := escapeLen(esc, i)
		if n == 0 {
			n = 1
		}
		seq := esc[i : i+n]
		if isSGR(seq) {
			if isSGRReset(seq) {
				s.active = ""
			} else {
				s.active += seq
			}
		}
		i += n
	}
}

// Wrap word-wraps text to width columns. Explicit newlines are honoured,
// words longer than width are hard-broken, and styling active at a break is
// carried onto the next line.
func Wrap(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	var st sgrState
	for _, logical := range strings.Split(text, "\n") {
		out = append(out, wrapLine(logical, width, &st)...)
	}
	return out
}

func wrapLine(line string, width int, st *sgrState) []string {
	cs, trailing := cells(line)
	if len(cs) == 0 {
		st.feed(trailing)
		return []string{trailing}
	}

	var lines []string
	var cur strings.Builder
	curW := 0
	start := func() {
		cur.Reset()
		cur.WriteString(st.active)
		curW = 0
	}
	flush := func() {
		lines = append(lines, cur.String())
	}
	start()

	isSpace := func(c cell) bool { return c.text == " " }

	for i := 0; i < len(cs); {
		// Collect the next token: a run of spaces or a run of non-spaces.
		j := i
		sp := isSpace(cs[i])
		tokW := 0
		for j < len(cs) && isSpace(cs[j]) == sp {
			tokW += cs[j].width
			j++
		}
		tok := cs[i:j]
		i = j

		if sp {
			// Spaces at a break point are dropped; otherwise kept if they fit.
			for _, c := range tok {
				st.feed(c.esc)
				cur.WriteString(c.esc)
				if curW > 0 && curW+c.width <= width {
					cur.WriteString(c.text)
					curW += c.width
				}
			}
			continue
		}

		if curW+tokW > width && curW > 0 {
			flush()
			start()
		}
		for _, c := range tok {
			if curW+c.width > width && curW > 0 {
				flush()
				start()
			}
			st.feed(c.esc)
			cur.WriteString(c.esc)
			cur.WriteString(c.text)
			curW += c.width
		}
	}
	st.feed(trailing)
	cur.WriteString(trailing)
	flush()

	// Trim trailing spaces that were kept before a wrap.
	for k := range lines {
		lines[k] = strings.TrimRight(lines[k], " ")
	}
	return lines
}
