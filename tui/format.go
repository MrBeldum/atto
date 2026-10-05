package tui

import (
	"fmt"
	"strings"
	"time"
)

// FormatDuration is a short duration: 850ms, 12s, 3.4s, 2m05s.
func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute && d%time.Second == 0:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm%02ds", m, int(d.Seconds())-60*m)
	}
}

// FormatTokens is a token count: 950, 12.5k, 1.2M.
func FormatTokens(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// FirstLine is s up to its first newline, with " …" if it went on.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
