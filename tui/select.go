package tui

import (
	"strconv"
	"strings"
)

type SelectItem struct {
	Label  string
	Detail string
	Value  string
	// Data carries the caller's own payload (a command, a session summary),
	// so callbacks need not look the item up again by Value.
	Data any
}

// SelectList is a vertical picker, shared by the slash-command list, the
// ordinary selectors and /resume (pi's SelectList plays the same roles).
// Up/Down (or Ctrl+P/N) move and wrap around, PageUp/PageDown jump, Enter
// picks, Escape cancels. At most MaxVisible rows show, in a window that keeps
// the selection centred, with a (n/m) indicator once it scrolls.
type SelectList struct {
	Title      string
	Items      []SelectItem
	Selected   int // index into the visible (filtered) items
	MaxVisible int // rows shown at once; 0 means 10
	// Filterable enables type-to-filter on label, detail and value.
	Filterable bool
	// FilterPrompt and FilterHint word the filter line; "Filter: " and
	// "type to filter" by default.
	FilterPrompt, FilterHint string
	// Empty is shown when nothing matches; "  no matches" by default.
	Empty string
	// Match decides whether an item matches the filter text; by default a
	// case-insensitive substring match on label, detail and value.
	Match func(it SelectItem, query string) bool
	// Source drives the list from outside text, such as the editor's, instead
	// of its own filter: the list shows no title or filter line, consumes no
	// typing, shows nothing at all when no item matches, and returns to the
	// first item whenever the text changes. Match then always applies.
	Source func() string
	// Indent prefixes every row and the indicator, to line rows up with
	// something above, such as the editor's text.
	Indent string
	// LabelWidth fixes the label column; 0 fits the widest visible label.
	LabelWidth int
	// RenderRow replaces the default row (marker, label, detail) with its own
	// lines, e.g. a two-line row.
	RenderRow func(it SelectItem, selected bool, width int) []string
	OnSelect  func(SelectItem)
	OnCancel  func()

	query   string
	lastSrc string
}

// text is the filter text: the external source if there is one.
func (s *SelectList) text() string {
	if s.Source != nil {
		return s.Source()
	}
	return s.query
}

// Query is the filter text, for callers that draw the filter themselves.
func (s *SelectList) Query() string { return s.text() }

func (s *SelectList) match(it SelectItem, q string) bool {
	if s.Match != nil {
		return s.Match(it, q)
	}
	return strings.Contains(strings.ToLower(StripEscapes(it.Label+" "+it.Detail+" "+it.Value)), strings.ToLower(q))
}

// Visible returns the items that pass the filter. It also keeps Selected in
// range and resets it when an external text source changed.
func (s *SelectList) Visible() []SelectItem {
	q := s.text()
	if s.Source != nil && q != s.lastSrc {
		s.lastSrc, s.Selected = q, 0
	}
	items := s.Items
	if q != "" || (s.Source != nil && s.Match != nil) {
		items = nil
		for _, it := range s.Items {
			if s.match(it, q) {
				items = append(items, it)
			}
		}
	}
	// After the items shrink, stay near where the selection was.
	s.Selected = max(0, min(s.Selected, len(items)-1))
	return items
}

// Current returns the selected item, if any item is visible.
func (s *SelectList) Current() (SelectItem, bool) {
	items := s.Visible()
	if len(items) == 0 {
		return SelectItem{}, false
	}
	return items[s.Selected], true
}

// Move selects the previous (-1) or next (1) item, wrapping around.
func (s *SelectList) Move(delta int) {
	if n := len(s.Visible()); n > 0 {
		s.Selected = (s.Selected + delta + n) % n
	}
}

func (s *SelectList) maxVisible() int {
	if s.MaxVisible > 0 {
		return s.MaxVisible
	}
	return 10
}

func (s *SelectList) HandleInput(data string) {
	items := s.Visible()
	n := len(items)
	switch Key(data) {
	case "up", "ctrl+p":
		s.Move(-1)
	case "down", "ctrl+n", "tab":
		s.Move(1)
	case "pageup":
		s.Selected = max(0, s.Selected-s.maxVisible())
	case "pagedown":
		s.Selected = min(max(0, n-1), s.Selected+s.maxVisible())
	case "enter":
		if s.OnSelect != nil && n > 0 {
			s.OnSelect(items[s.Selected])
		}
	case "escape":
		if s.query != "" {
			s.query, s.Selected = "", 0
		} else if s.OnCancel != nil {
			s.OnCancel()
		}
	case "ctrl+c":
		if s.OnCancel != nil {
			s.OnCancel()
		}
	case "backspace":
		if r := []rune(s.query); s.canFilter() && len(r) > 0 {
			s.query, s.Selected = string(r[:len(r)-1]), 0
		}
	default:
		if s.canFilter() && Printable(data) {
			s.query += data
			s.Selected = 0
		}
	}
}

func (s *SelectList) canFilter() bool { return s.Filterable && s.Source == nil }

// window returns the range of items to show: it keeps the selection centred
// (pi's getVisibleRange).
func window(sel, n, maxVis int) (int, int) { return Window(sel, n, maxVis) }

// Window is the range of n items shown in maxVis rows around sel, for
// callers that draw the rows themselves.
func Window(sel, n, maxVis int) (int, int) {
	start := max(0, min(sel-maxVis/2, n-maxVis))
	return start, min(start+maxVis, n)
}

func (s *SelectList) Render(width int) []string {
	items := s.Visible()
	var out []string
	if s.Source == nil {
		if s.Title != "" {
			out = append(out, Truncate(Dim(s.Title), width, "…"))
		}
		if s.Filterable {
			prompt, hint := s.FilterPrompt, s.FilterHint
			if prompt == "" {
				prompt = "Filter: "
			}
			if hint == "" {
				hint = "type to filter"
			}
			q := Dim(hint)
			if s.query != "" {
				q = s.query
			}
			out = append(out, Truncate(Dim(prompt)+q, width, "…"))
		}
	}
	if len(items) == 0 {
		if s.Source != nil {
			return nil
		}
		empty := s.Empty
		if empty == "" {
			empty = "  no matches"
		}
		return append(out, Dim(empty))
	}
	start, end := window(s.Selected, len(items), s.maxVisible())
	labelW := s.LabelWidth
	if labelW == 0 {
		for _, it := range items {
			labelW = max(labelW, VisibleWidth(it.Label))
		}
	}
	for i := start; i < end; i++ {
		it, sel := items[i], i == s.Selected
		if s.RenderRow != nil {
			out = append(out, s.RenderRow(it, sel, width)...)
			continue
		}
		pad := max(0, labelW-VisibleWidth(it.Label))
		label := it.Label + strings.Repeat(" ", pad)
		line := s.Indent + "  " + label
		if sel {
			line = s.Indent + FG(6, "› "+label)
		}
		if it.Detail != "" {
			sep := ""
			if s.LabelWidth == 0 {
				sep = "  "
			} else if pad == 0 {
				sep = " "
			}
			line += sep + Dim(it.Detail)
		}
		out = append(out, Truncate(line, width, "…"))
	}
	if start > 0 || end < len(items) {
		out = append(out, Dim(s.Indent+"  ("+strconv.Itoa(s.Selected+1)+"/"+strconv.Itoa(len(items))+")"))
	}
	return out
}
