package jobs

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/events"
)

const (
	// DefaultNotifyLimit caps notification events per job.
	DefaultNotifyLimit = 50
	// notifyBatch is how long matches are collected into one event, so a
	// burst of errors wakes the agent once instead of once per line.
	notifyBatch = time.Second
	// notifyLine truncates each matching line in an event.
	notifyLine = 300
	// notifyLines caps lines per event; the rest stay in the log.
	notifyLines = 20
)

// Notify makes a job post an event for output lines matching Pattern while
// it keeps running (like Claude Code's Monitor tool).
type Notify struct {
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit,omitempty"` // max events; 0 = DefaultNotifyLimit
}

// notifier is an io.Writer in front of the output log: it passes bytes
// through and scans complete lines for matches.
type notifier struct {
	out     io.Writer
	j       Job
	re      *regexp.Regexp
	limit   int
	window  time.Duration
	post    func(events.Event)
	mu      sync.Mutex
	partial []byte
	batch   []string
	more    int // matches beyond notifyLines in this batch
	timer   *time.Timer
	posted  int
	capped  bool
}

func newNotifier(out io.Writer, j Job, post func(events.Event)) *notifier {
	limit := j.Notify.Limit
	if limit <= 0 {
		limit = DefaultNotifyLimit
	}
	return &notifier{out: out, j: j, re: regexp.MustCompile(j.Notify.Pattern), limit: limit, window: notifyBatch, post: post}
}

func (n *notifier) Write(p []byte) (int, error) {
	w, err := n.out.Write(p)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.capped {
		return w, err
	}
	n.partial = append(n.partial, p...)
	for {
		i := bytes.IndexByte(n.partial, '\n')
		if i < 0 {
			break
		}
		n.line(string(n.partial[:i]))
		n.partial = n.partial[i+1:]
	}
	// A line with no newline yet could grow without bound; match what we have.
	if len(n.partial) > 64<<10 {
		n.line(string(n.partial))
		n.partial = nil
	}
	return w, err
}

func (n *notifier) line(s string) {
	s = strings.TrimRight(s, "\r")
	if n.capped || !n.re.MatchString(s) {
		return
	}
	if len(n.batch) >= notifyLines {
		n.more++
		return
	}
	if len(s) > notifyLine {
		s = s[:notifyLine] + " …"
	}
	n.batch = append(n.batch, s)
	if n.timer == nil {
		n.timer = time.AfterFunc(n.window, func() {
			n.mu.Lock()
			defer n.mu.Unlock()
			n.flush()
		})
	}
}

// Close flushes a final unterminated line and any pending batch, so the
// last matches arrive before the exit event.
func (n *notifier) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.partial) > 0 {
		n.line(string(n.partial))
		n.partial = nil
	}
	n.flush()
}

// flush posts the pending batch; n.mu must be held.
func (n *notifier) flush() {
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	if len(n.batch) == 0 || n.capped {
		n.batch, n.more = nil, 0
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Background job %d (%q) output matched /%s/:", n.j.ID, n.j.Label(), n.j.Notify.Pattern)
	for _, l := range n.batch {
		b.WriteString("\n" + l)
	}
	if n.more > 0 {
		fmt.Fprintf(&b, "\n… and %d more matching lines", n.more)
	}
	n.posted++
	if n.posted >= n.limit {
		n.capped = true
		fmt.Fprintf(&b, "\nNotifications for this job are capped at %d events; no more will be sent. The job is still running: atto job output %d has the rest.", n.limit, n.j.ID)
	}
	title := fmt.Sprintf("◎ job %d: %s", n.j.ID, n.batch[0])
	n.batch, n.more = nil, 0
	n.post(events.Event{Source: "job", Text: b.String(), Title: title})
}
