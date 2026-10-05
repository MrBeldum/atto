package tui

import "strconv"

// Minimal SGR helpers. Each wraps s and resets only the attribute it set, so
// they nest.

func Bold(s string) string   { return "\x1b[1m" + s + "\x1b[22m" }
func Dim(s string) string    { return "\x1b[2m" + s + "\x1b[22m" }
func Italic(s string) string { return "\x1b[3m" + s + "\x1b[23m" }

// FG colors s with a 256-color palette index.
func FG(n int, s string) string { return "\x1b[38;5;" + strconv.Itoa(n) + "m" + s + "\x1b[39m" }

// BG sets a 256-color background on s.
func BG(n int, s string) string { return "\x1b[48;5;" + strconv.Itoa(n) + "m" + s + "\x1b[49m" }
