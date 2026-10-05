package tui

import (
	"strconv"
	"strings"
)

type SelectItem struct {
	Label  string
	Detail string
	Value  string
}

// SelectList is a vertical picker. Up/Down (or Ctrl+P/N) move, Enter picks,
// Escape cancels.
type SelectList struct {
	Title      string
	Items      []SelectItem
	Selected   int
	MaxVisible int
	// Filterable enables type-to-filter on label and detail.
	Filterable bool
	OnSelect   func(SelectItem)
	OnCancel   func()

	query string
}

func (s *SelectList) visible() []SelectItem {
	if s.query == "" {
		return s.Items
	}
	q := strings.ToLower(s.query)
	var out []SelectItem
	for _, it := range s.Items {
		if strings.Contains(strings.ToLower(StripEscapes(it.Label+" "+it.Detail+" "+it.Value)), q) {
			out = append(out, it)
		}
	}
	return out
}

func (s *SelectList) HandleInput(data string) {
	items := s.visible()
	switch Key(data) {
	case "up", "ctrl+p":
		if len(items) > 0 {
			s.Selected = (s.Selected - 1 + len(items)) % len(items)
		}
	case "down", "ctrl+n", "tab":
		if len(items) > 0 {
			s.Selected = (s.Selected + 1) % len(items)
		}
	case "enter":
		if s.OnSelect != nil && s.Selected < len(items) {
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
		if r := []rune(s.query); s.Filterable && len(r) > 0 {
			s.query, s.Selected = string(r[:len(r)-1]), 0
		}
	default:
		if s.Filterable && Printable(data) {
			s.query += data
			s.Selected = 0
		}
	}
}

func (s *SelectList) Render(width int) []string {
	var out []string
	if s.Title != "" {
		out = append(out, Truncate(Dim(s.Title), width, "…"))
	}
	if s.Filterable {
		q := Dim("type to filter")
		if s.query != "" {
			q = s.query
		}
		out = append(out, Truncate(Dim("Filter: ")+q, width, "…"))
	}
	all := s.Items
	s.Items = s.visible()
	defer func() { s.Items = all }()
	if len(s.Items) == 0 {
		return append(out, Dim("  no matches"))
	}
	maxVis := s.MaxVisible
	if maxVis <= 0 {
		maxVis = 10
	}
	start := 0
	if s.Selected >= maxVis {
		start = s.Selected - maxVis + 1
	}
	end := min(len(s.Items), start+maxVis)
	labelW := 0
	for _, it := range s.Items {
		labelW = max(labelW, VisibleWidth(it.Label))
	}
	for i := start; i < end; i++ {
		it := s.Items[i]
		label := it.Label + strings.Repeat(" ", labelW-VisibleWidth(it.Label))
		line := "  " + label
		if i == s.Selected {
			line = FG(6, "› "+label)
		}
		if it.Detail != "" {
			line += "  " + Dim(it.Detail)
		}
		out = append(out, Truncate(line, width, "…"))
	}
	if end < len(s.Items) || start > 0 {
		out = append(out, Dim("  ("+strconv.Itoa(s.Selected+1)+"/"+strconv.Itoa(len(s.Items))+")"))
	}
	return out
}
