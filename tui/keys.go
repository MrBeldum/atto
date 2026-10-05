package tui

import (
	"strings"
	"unicode/utf8"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// PastePrefix marks an input event that carries bracketed-paste content.
// The event is PastePrefix followed by the pasted text.
const PastePrefix = "\x00paste:"

// inputParser splits raw stdin bytes into individual key sequences and
// reassembles bracketed pastes. Incomplete sequences are held until the next
// read.
type inputParser struct {
	pending string
	paste   *strings.Builder
}

func (p *inputParser) feed(data string) []string {
	s := p.pending + data
	p.pending = ""
	var out []string
	for len(s) > 0 {
		if p.paste != nil {
			end := strings.Index(s, pasteEnd)
			if end < 0 {
				p.paste.WriteString(s)
				return out
			}
			p.paste.WriteString(s[:end])
			out = append(out, PastePrefix+p.paste.String())
			p.paste = nil
			s = s[end+len(pasteEnd):]
			continue
		}
		if strings.HasPrefix(s, pasteStart) {
			p.paste = &strings.Builder{}
			s = s[len(pasteStart):]
			continue
		}
		n, complete := seqLen(s)
		if !complete {
			p.pending = s
			return out
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// seqLen returns the length of the first key sequence in s and whether it is
// complete. A lone ESC at the end of a read is treated as the Escape key.
func seqLen(s string) (int, bool) {
	if s[0] != 0x1b {
		if !utf8.FullRuneInString(s) {
			return 0, false
		}
		_, n := utf8.DecodeRuneInString(s)
		return n, true
	}
	if len(s) == 1 {
		return 1, true
	}
	switch s[1] {
	case '[':
		for j := 2; j < len(s); j++ {
			if c := s[j]; c >= 0x40 && c <= 0x7e {
				return j + 1, true
			}
		}
		return 0, false
	case 'O':
		if len(s) < 3 {
			return 0, false
		}
		return 3, true
	default:
		// Alt+key: ESC followed by one rune.
		if !utf8.FullRuneInString(s[1:]) {
			return 0, false
		}
		_, n := utf8.DecodeRuneInString(s[1:])
		return 1 + n, true
	}
}

// Key names a decoded key sequence, e.g. "enter", "ctrl+c", "left", "alt+b".
// Printable input returns "" — use the raw data instead.
func Key(data string) string {
	switch data {
	case "\r":
		return "enter"
	case "\n":
		return "ctrl+j"
	case "\t":
		return "tab"
	case "\x1b[Z":
		return "shift+tab"
	case "\x7f", "\x08":
		return "backspace"
	case "\x1b":
		return "escape"
	case "\x1b\r":
		return "alt+enter"
	case "\x1b[A", "\x1bOA":
		return "up"
	case "\x1b[B", "\x1bOB":
		return "down"
	case "\x1b[C", "\x1bOC":
		return "right"
	case "\x1b[D", "\x1bOD":
		return "left"
	case "\x1b[H", "\x1bOH", "\x1b[1~":
		return "home"
	case "\x1b[F", "\x1bOF", "\x1b[4~":
		return "end"
	case "\x1b[3~":
		return "delete"
	case "\x1b[5~":
		return "pageup"
	case "\x1b[6~":
		return "pagedown"
	case "\x1b[1;2D":
		return "shift+left"
	case "\x1b[1;2C":
		return "shift+right"
	case "\x1b[1;5C", "\x1bf":
		return "word-right"
	case "\x1b[1;5D", "\x1bb":
		return "word-left"
	case "\x1b\x7f":
		return "alt+backspace"
	}
	if len(data) == 1 && data[0] < 0x20 {
		return "ctrl+" + string(rune('a'+data[0]-1))
	}
	return ""
}

// Printable reports whether data is plain text to insert.
func Printable(data string) bool {
	if data == "" || strings.HasPrefix(data, PastePrefix) {
		return false
	}
	for _, r := range data {
		if r < 0x20 || r == 0x7f || r == 0x1b {
			return false
		}
	}
	return true
}
