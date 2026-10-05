package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/shell"
)

const (
	DefaultBashTimeout = 60 * time.Second
	MaxBashTimeout     = 30 * time.Minute

	// What goes back to the model, as in codex: about 10k tokens (4 bytes
	// per token), cut from the middle so both the start (the first error)
	// and the end (the summary) survive. The full output is saved to a file.
	maxOutputBytes = 40_000
	// Hard cap on what is buffered in memory per command.
	maxCaptureBytes = 8 * 1024 * 1024
)

var bashSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "description": {
      "type": "string",
      "description": "What this command does, in a few words, shown to the user. E.g. \"JIT compile atto.py\", \"Run unit tests\", \"Read main.go\"."
    },
    "command": {
      "type": "string",
      "description": "The bash command to run."
    },
    "timeout": {
      "type": "integer",
      "description": "Timeout in seconds. Default 60. Raise it for long builds or test suites."
    }
  },
  "required": ["description", "command"]
}`)

// toolDescription describes the shell tool for the model.
func toolDescription(sh shell.Shell) string {
	var lead string
	switch sh.Kind {
	case shell.PowerShell:
		lead = "Run a PowerShell command in the working directory and return its combined output. " +
			"Each call runs in a fresh PowerShell session (use absolute paths or `Set-Location dir; ...`). "
	case shell.Cmd:
		lead = "Run a cmd.exe command in the working directory and return its combined output. " +
			"Each call runs in a fresh shell (use absolute paths or `cd /d dir && ...`). "
	default:
		lead = "Run a bash command in the working directory and return its combined stdout/stderr. " +
			"Each call runs in a fresh shell (use absolute paths or `cd dir && ...`). "
	}
	return lead + "Stdin is not connected; do not start interactive programs. " +
		"Output over about 10k tokens is cut from the middle (the start and end are kept), and the full output is saved to a file whose path is reported."
}

type BashArgs struct {
	Description string `json:"description"`
	Command     string `json:"command"`
	Timeout     int    `json:"timeout,omitempty"`
}

func (a BashArgs) timeout() time.Duration {
	if a.Timeout <= 0 {
		return DefaultBashTimeout
	}
	return min(time.Duration(a.Timeout)*time.Second, MaxBashTimeout)
}

type BashResult struct {
	Output   string // raw combined output (possibly capped)
	ExitCode int
	TimedOut bool
	Canceled bool
	Duration time.Duration
	Err      error // failure to start, etc.
}

// streamWriter collects output and forwards chunks to a callback.
type streamWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	dropped  int
	onOutput func(string)
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if room := maxCaptureBytes - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(len(p), room)])
		w.dropped += max(0, len(p)-room)
	} else {
		w.dropped += len(p)
	}
	w.mu.Unlock()
	if w.onOutput != nil {
		w.onOutput(string(p))
	}
	return len(p), nil
}

// RunBash runs args.Command with the default shell (bash on Unix,
// PowerShell on Windows).
func RunBash(ctx context.Context, cwd string, env []string, args BashArgs, onOutput func(string)) BashResult {
	return RunShell(ctx, shell.Default(), cwd, env, args, onOutput)
}

// RunShell executes args.Command with sh in cwd. The whole process tree
// (process group on Unix, job object on Windows) is killed on timeout or
// when ctx is canceled.
func RunShell(ctx context.Context, sh shell.Shell, cwd string, env []string, args BashArgs, onOutput func(string)) BashResult {
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, args.timeout())
	defer cancel()

	cmd := sh.Command(tctx, args.Command)
	cmd.Dir = cwd
	cmd.Env = append(append(os.Environ(), "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat", "NO_COLOR=1"), env...)
	tree := shell.NewTree(cmd)
	defer tree.Close()
	cmd.WaitDelay = 2 * time.Second // don't hang on pipes held by orphaned children
	w := &streamWriter{onOutput: onOutput}
	cmd.Stdout, cmd.Stderr = w, w

	err := cmd.Start()
	if err == nil {
		tree.Started()
		err = cmd.Wait()
	}
	res := BashResult{Duration: time.Since(start)}
	w.mu.Lock()
	res.Output = w.buf.String()
	if w.dropped > 0 {
		res.Output += fmt.Sprintf("\n[%d bytes of output dropped]", w.dropped)
	}
	w.mu.Unlock()

	switch {
	case ctx.Err() != nil:
		res.Canceled = true
		res.ExitCode = -1
	case errors.Is(tctx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.ExitCode = -1
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		} else if !errors.Is(err, exec.ErrWaitDelay) {
			res.Err = err
			res.ExitCode = -1
		}
	}
	return res
}

// ForModel formats the result as the tool message content.
func (r BashResult) ForModel(args BashArgs) string {
	if r.Err != nil {
		return "error: " + r.Err.Error()
	}
	out := truncateMiddle(tidy(r.Output))
	var b strings.Builder
	b.WriteString(out)
	if out != "" && !strings.HasSuffix(out, "\n") {
		b.WriteString("\n")
	}
	switch {
	case r.Canceled:
		b.WriteString("[canceled by user]")
	case r.TimedOut:
		fmt.Fprintf(&b, "[timed out after %s; pass a larger timeout if the command needs more time]", args.timeout())
	case r.ExitCode != 0:
		fmt.Fprintf(&b, "[exit code %d]", r.ExitCode)
	case out == "":
		b.WriteString("[no output]")
	}
	return strings.TrimRight(b.String(), "\n")
}

// tidy normalizes output for the model: CRLF to LF, and no trailing
// whitespace-only lines (PowerShell pads tables with them).
func tidy(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// truncateMiddle keeps the first and last maxOutputBytes/2 bytes (on line
// boundaries) and saves the full text to a temp file when it had to cut.
func truncateMiddle(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	half := maxOutputBytes / 2
	head := s[:half]
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i]
	}
	tail := s[len(s)-half:]
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i < len(tail)-1 {
		tail = tail[i+1:]
	}
	total := strings.Count(s, "\n") + 1
	cut := total - (strings.Count(head, "\n") + 1) - (strings.Count(tail, "\n") + 1)
	note := fmt.Sprintf("[output truncated: %d lines, ~%d tokens; showing the start and the end", total, len(s)/4)
	if f, err := os.CreateTemp("", "atto-bash-*.log"); err == nil {
		_, _ = f.WriteString(s)
		f.Close()
		note += "; full output: " + f.Name()
	}
	return fmt.Sprintf("%s]\n%s\n[… %d lines omitted …]\n%s", note, head, max(cut, 0), tail)
}
