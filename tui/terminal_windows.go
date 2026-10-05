//go:build windows

package tui

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// consoleState holds the console modes to restore on exit.
type consoleState struct {
	in, out         windows.Handle
	inMode, outMode uint32
	inCP, outCP     uint32
	ok              bool
}

// enableVT turns on VT processing so the console understands the same
// escape sequences as Unix terminals (Windows 10 1511+): ANSI output, and
// VT-encoded keys (arrows, Shift+Tab, bracketed paste, mouse) on input.
func enableVT(in, out *os.File) consoleState {
	s := consoleState{in: windows.Handle(in.Fd()), out: windows.Handle(out.Fd())}
	if windows.GetConsoleMode(s.in, &s.inMode) != nil || windows.GetConsoleMode(s.out, &s.outMode) != nil {
		return s
	}
	s.ok = true
	// Use UTF-8 code pages while atto runs. Under a legacy code page (437 on
	// English Windows) conhost measures CJK text differently from the
	// terminal drawing it, and wide characters can come out doubled.
	s.inCP, _ = windows.GetConsoleCP()
	s.outCP, _ = windows.GetConsoleOutputCP()
	_ = windows.SetConsoleCP(cpUTF8)
	_ = windows.SetConsoleOutputCP(cpUTF8)
	_ = windows.SetConsoleMode(s.out, s.outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN|windows.ENABLE_PROCESSED_OUTPUT)
	// term.MakeRaw already ran; add VT input on top of the raw mode.
	var raw uint32
	if windows.GetConsoleMode(s.in, &raw) == nil {
		_ = windows.SetConsoleMode(s.in, raw|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	}
	return s
}

func restoreVT(s consoleState) {
	if !s.ok {
		return
	}
	_ = windows.SetConsoleMode(s.out, s.outMode)
	if s.outCP != 0 {
		_ = windows.SetConsoleOutputCP(s.outCP)
	}
	if s.inCP != 0 {
		_ = windows.SetConsoleCP(s.inCP)
	}
}

// watchResize polls the console size: Windows has no SIGWINCH.
func watchResize(t *ProcessTerminal, done <-chan struct{}, onResize func()) {
	w, h := t.Size()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			if nw, nh := t.Size(); nw != w || nh != h {
				w, h = nw, nh
				onResize()
			}
		}
	}
}

// cpUTF8 is the UTF-8 code page.
const cpUTF8 = 65001
