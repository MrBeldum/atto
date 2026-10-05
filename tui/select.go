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
	OnSelect   func(SelectItem)
	OnCancel   func()
}

func (s *SelectList) HandleInput(data string) {
	switch Key(data) {
	case "up", "ctrl+p":
		if len(s.Items) > 0 {
			s.Selected = (s.Selected - 1 + len(s.Items)) % len(s.Items)
		}
	case "down", "ctrl+n", "tab":
		if len(s.Items) > 0 {
			s.Selected = (s.Selected + 1) % len(s.Items)
		}
	case "enter":
		if s.OnSelect != nil && s.Selected < len(s.Items) {
			s.OnSelect(s.Items[s.Selected])
		}
	case "escape", "ctrl+c":
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
}

func (s *SelectList) Render(width int) []string {
	var out []string
	if s.Title != "" {
		out = append(out, Truncate(Dim(s.Title), width, "…"))
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
