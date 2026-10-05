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
	"syscall"
	"time"
)

const (
	DefaultBashTimeout = 60 * time.Second
	MaxBashTimeout     = 30 * time.Minute

	// Limits on what goes back to the model; the full output is saved to a file.
	maxOutputBytes = 50 * 1024
	maxOutputLines = 2000
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

const bashDescription = "Run a bash command in the working directory and return its combined stdout/stderr. " +
	"Each call runs in a fresh shell (use absolute paths or `cd dir && ...`). " +
	"Stdin is not connected; do not start interactive programs. " +
	"Output beyond 2000 lines or 50KB is truncated to the tail, and the full output is saved to a file whose path is reported."

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

// RunBash executes args.Command with bash in cwd. The whole process group is
// killed on timeout or when ctx is canceled.
func RunBash(ctx context.Context, cwd string, env []string, args BashArgs, onOutput func(string)) BashResult {
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, args.timeout())
	defer cancel()

	cmd := exec.CommandContext(tctx, "bash", "-c", args.Command)
	cmd.Dir = cwd
	cmd.Env = append(append(os.Environ(), "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat"), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second // don't hang on pipes held by orphaned children
	w := &streamWriter{onOutput: onOutput}
	cmd.Stdout, cmd.Stderr = w, w

	err := cmd.Run()
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
	out := truncateTail(r.Output)
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

// truncateTail keeps the last maxOutputLines lines / maxOutputBytes bytes and
// saves the full text to a temp file when it had to cut.
func truncateTail(s string) string {
	lines := strings.Split(s, "\n")
	if len(s) <= maxOutputBytes && len(lines) <= maxOutputLines {
		return s
	}
	keep := lines[max(0, len(lines)-maxOutputLines):]
	tail := strings.Join(keep, "\n")
	if len(tail) > maxOutputBytes {
		tail = tail[len(tail)-maxOutputBytes:]
		if i := strings.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
	}
	shown := strings.Count(tail, "\n") + 1
	note := fmt.Sprintf("[output truncated: showing last %d of %d lines", shown, len(lines))
	if f, err := os.CreateTemp("", "atto-bash-*.log"); err == nil {
		_, _ = f.WriteString(s)
		f.Close()
		note += "; full output: " + f.Name()
	}
	return note + "]\n" + tail
}
