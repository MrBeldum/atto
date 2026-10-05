// Package session persists conversations as append-only JSONL files under
// ~/.atto/sessions/YYYY/MM/DD/<YYYYMMDD-HHMMSS>-<id>.jsonl.
//
// The first line is a "session" header. Every later line is one Entry,
// written and flushed as it happens, so a crash loses at most the line
// being written. Replaying the entries in order reconstructs the
// conversation: a "compaction" entry replaces all earlier messages with the
// handoff notes.
package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/provider"
)

const Version = 1

// Entry types.
const (
	TypeSession    = "session"
	TypeMessage    = "message"
	TypeCompaction = "compaction"
	TypeModel      = "model"
	TypeEffort     = "effort"
	TypeName       = "name"
	TypeGoal       = "goal"
)

// Entry is one line of a session file. Fields are used according to Type.
type Entry struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`

	// session header
	Version int    `json:"version,omitempty"`
	ID      string `json:"id,omitempty"`
	Cwd     string `json:"cwd,omitempty"`

	// message
	Message    *provider.Message `json:"message,omitempty"`
	Usage      *provider.Usage   `json:"usage,omitempty"`      // assistant messages
	ThinkingMs int64             `json:"thinkingMs,omitempty"` // assistant messages
	Tool       *ToolMeta         `json:"tool,omitempty"`       // tool messages

	// compaction: Replacement is the full message history after compaction.
	Replacement  []provider.Message `json:"replacement,omitempty"`
	Notes        string             `json:"notes,omitempty"`
	TokensBefore int                `json:"tokensBefore,omitempty"`
	Auto         bool               `json:"auto,omitempty"`

	// model / effort
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`

	// name
	Name string `json:"name,omitempty"`

	// goal: a snapshot of the session's goal (null when cleared)
	Goal json.RawMessage `json:"goal,omitempty"`
}

// ToolMeta records how a tool call went, for redisplay on resume.
type ToolMeta struct {
	Description string `json:"description,omitempty"`
	ExitCode    int    `json:"exitCode"`
	DurationMs  int64  `json:"durationMs"`
	TimedOut    bool   `json:"timedOut,omitempty"`
	Canceled    bool   `json:"canceled,omitempty"`
}

// Writer appends entries to a session file. The file is created lazily on
// the first entry so empty sessions leave nothing behind.
type Writer struct {
	mu      sync.Mutex
	ID      string
	Path    string
	cwd     string
	created time.Time
	f       *os.File
	err     error
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// New prepares a writer for a fresh session in cwd.
func New(cwd string) *Writer {
	now := time.Now()
	id := newID()
	path := filepath.Join(config.SessionsDir(), now.Format("2006/01/02"), now.Format("20060102-150405")+"-"+id+".jsonl")
	return &Writer{ID: id, Path: path, cwd: cwd, created: now}
}

// Resume returns a writer that appends to an existing session file.
func Resume(path string, h Entry) *Writer {
	return &Writer{ID: h.ID, Path: path, cwd: h.Cwd, created: h.Time}
}

func (w *Writer) open() error {
	if w.f != nil || w.err != nil {
		return w.err
	}
	if err := os.MkdirAll(filepath.Dir(w.Path), 0o755); err != nil {
		w.err = err
		return err
	}
	_, statErr := os.Stat(w.Path)
	f, err := os.OpenFile(w.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		w.err = err
		return err
	}
	w.f = f
	if statErr == nil {
		// Existing file: if a crash left a partial last line, terminate it so
		// new entries start on their own line.
		if st, err := f.Stat(); err == nil && st.Size() > 0 {
			last := make([]byte, 1)
			if r, err := os.Open(w.Path); err == nil {
				_, _ = r.ReadAt(last, st.Size()-1)
				r.Close()
			}
			if last[0] != '\n' {
				_, _ = f.Write([]byte{'\n'})
			}
		}
	}
	if statErr != nil { // new file: write the header
		return w.write(Entry{Type: TypeSession, Time: w.created, Version: Version, ID: w.ID, Cwd: w.cwd})
	}
	return nil
}

func (w *Writer) write(e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = w.f.Write(append(line, '\n'))
	return err
}

// Append writes e, stamping the time if unset. Errors are sticky and
// reported by Err; persistence never interrupts the conversation.
func (w *Writer) Append(e Entry) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if err := w.open(); err != nil {
		return
	}
	if err := w.write(e); err != nil && w.err == nil {
		w.err = err
	}
}

func (w *Writer) Err() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *Writer) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
}

// Load reads a session file: the header and the entries after it.
// A truncated last line (from a crash mid-write) is ignored.
func Load(path string) (Entry, []Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	var header Entry
	var entries []Entry
	for first := true; sc.Scan(); first = false {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if first {
			if e.Type != TypeSession {
				return Entry{}, nil, fmt.Errorf("%s: not an atto session", path)
			}
			header = e
			continue
		}
		entries = append(entries, e)
	}
	return header, entries, sc.Err()
}

// Summary describes a stored session for the resume picker.
type Summary struct {
	Path     string
	ID       string
	Name     string // from the latest "name" entry
	Archived bool
	Cwd      string
	Created  time.Time
	Updated  time.Time
	Preview  string // first user message
	Messages int    // user + assistant messages
}

// List returns sessions, newest first. If cwd is non-empty only sessions
// started in that directory are returned. archived selects archived
// sessions instead of active ones.
func List(cwd string, archived bool) ([]Summary, error) {
	var out []Summary
	root := config.SessionsDir()
	if archived {
		root = config.ArchivedDir()
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		s, err := summarize(path)
		if err != nil || (cwd != "" && s.Cwd != cwd) || s.Messages == 0 {
			return nil
		}
		s.Archived = archived
		out = append(out, s)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func summarize(path string) (Summary, error) {
	h, entries, err := Load(path)
	if err != nil {
		return Summary{}, err
	}
	s := Summary{Path: path, ID: h.ID, Cwd: h.Cwd, Created: h.Time, Updated: h.Time}
	for _, e := range entries {
		s.Updated = e.Time
		if e.Type == TypeName {
			s.Name = e.Name
		}
		if e.Type != TypeMessage || e.Message == nil {
			continue
		}
		switch e.Message.Role {
		case "user":
			s.Messages++
			if s.Preview == "" {
				s.Preview = e.Message.Content
			}
		case "assistant":
			s.Messages++
		}
	}
	return s, nil
}

// Latest returns the most recently updated session for cwd.
func Latest(cwd string) (Summary, bool) {
	l, err := List(cwd, false)
	if err != nil || len(l) == 0 {
		return Summary{}, false
	}
	return l[0], true
}

// Archive moves an active session file into the archive, keeping its
// date layout. It returns the new path.
func Archive(path string) (string, error) {
	return move(path, config.SessionsDir(), config.ArchivedDir())
}

// Unarchive moves an archived session back. It returns the new path.
func Unarchive(path string) (string, error) {
	return move(path, config.ArchivedDir(), config.SessionsDir())
}

func move(path, fromRoot, toRoot string) (string, error) {
	rel, err := filepath.Rel(fromRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is not under %s", path, fromRoot)
	}
	dst := filepath.Join(toRoot, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	return dst, os.Rename(path, dst)
}
