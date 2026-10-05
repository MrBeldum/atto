package tui

import (
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// Terminal is the minimal surface the renderer needs. It is an interface so
// tests can substitute a virtual terminal.
type Terminal interface {
	// Start enters raw mode and begins delivering input and resize events.
	Start(onInput func(data string), onResize func()) error
	// Stop restores the terminal to its original state.
	Stop()
	Write(s string)
	Size() (cols, rows int)
}

// ProcessTerminal drives the real stdin/stdout.
type ProcessTerminal struct {
	in, out  *os.File
	oldState *term.State
	sigs     chan os.Signal
	done     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex // serializes writes
}

func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{in: os.Stdin, out: os.Stdout}
}

func (t *ProcessTerminal) Start(onInput func(string), onResize func()) error {
	st, err := term.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return err
	}
	t.oldState = st
	t.done = make(chan struct{})

	t.Write("\x1b[?2004h") // bracketed paste

	t.sigs = make(chan os.Signal, 1)
	signal.Notify(t.sigs, syscall.SIGWINCH)
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		for {
			select {
			case <-t.sigs:
				onResize()
			case <-t.done:
				return
			}
		}
	}()

	// The read loop is not joined on Stop: a blocking read on stdin cannot be
	// interrupted portably, and the process exits shortly after anyway.
	go func() {
		parser := &inputParser{}
		buf := make([]byte, 4096)
		for {
			n, err := t.in.Read(buf)
			if n > 0 {
				select {
				case <-t.done:
					return
				default:
				}
				for _, ev := range parser.feed(string(buf[:n])) {
					onInput(ev)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return nil
}

func (t *ProcessTerminal) Stop() {
	if t.oldState == nil {
		return
	}
	signal.Stop(t.sigs)
	close(t.done)
	t.wg.Wait()
	t.Write("\x1b[?2004l\x1b[?25h")
	_ = term.Restore(int(t.in.Fd()), t.oldState)
	t.oldState = nil
}

func (t *ProcessTerminal) Write(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = io.WriteString(t.out, s)
}

func (t *ProcessTerminal) Size() (int, int) {
	w, h, err := term.GetSize(int(t.out.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}
