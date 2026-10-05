package app

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/fsutil"
	"github.com/sebastianrcnt/atto/tui"
)

// "@" file mentions, like pi's: typing @ at the start of a word lists the
// project's files and folders, filtered fuzzily by what follows. Accepting
// one only inserts its path ("@src/main.go "); nothing is expanded on send,
// the model reads the file itself. The text rules are in tui/autocomplete.go.

const (
	// maxMentions is how many ranked files the list holds (pi: 20); it
	// shows maxSuggestions of them at once.
	maxMentions = 20
	// fileListTTL is how old the file list may get before typing @ again
	// walks the project anew.
	fileListTTL = 10 * time.Second
)

type mentions struct {
	list      *tui.SelectList
	prefix    string // the token under the cursor, while one is
	dismissed string // the editor text for which Esc closed the list
	open      bool
	ranked    mentionKey // what list.Items were ranked for
	cache     tui.RenderCache[mentionKey]

	// The project's files, filled in by a background walk.
	mu      sync.Mutex
	files   []fsutil.Entry
	gen     int // bumped whenever files changes
	walking bool
	walked  time.Time
}

type mentionKey struct {
	query string
	gen   int
	sel   int
}

// mentionList returns the "@" list, ranked for the token before the cursor;
// it has no items when there is no such token.
func (a *App) mentionList() *tui.SelectList {
	m := &a.mention
	if m.list == nil {
		m.list = &tui.SelectList{MaxVisible: maxSuggestions, Indent: " "}
		m.list.Source = func() string { return m.prefix }
		m.list.Match = func(tui.SelectItem, string) bool { return true }
		m.ranked = mentionKey{gen: -1}
	}
	prefix, ok := "", false
	if a.editor != nil {
		prefix, ok = tui.MentionPrefix(a.editor.LineBeforeCursor())
		ok = ok && a.editor.Text() != m.dismissed
	}
	if !ok {
		m.open, m.prefix, m.list.Items, m.ranked = false, "", nil, mentionKey{gen: -1}
		return m.list
	}
	if !m.open { // just typed @: list the files, or refresh an old list
		m.open = true
		a.walkFiles()
	}
	m.prefix = prefix
	query, _ := tui.ParseMention(prefix)
	query = strings.ReplaceAll(query, `\`, "/")
	m.mu.Lock()
	files, gen := m.files, m.gen
	m.mu.Unlock()
	if key := (mentionKey{query: query, gen: gen}); key != m.ranked {
		m.ranked = key
		m.list.Items, m.list.LabelWidth = mentionItems(rankFiles(files, query, maxMentions))
	}
	return m.list
}

// walkFiles lists the project's files in the background, unless a walk is
// running or the last one is recent. The first walk shows files as they
// are found; later ones swap the new list in when done.
func (a *App) walkFiles() {
	m := &a.mention
	m.mu.Lock()
	if m.walking || (!m.walked.IsZero() && time.Since(m.walked) < fileListTTL) {
		m.mu.Unlock()
		return
	}
	m.walking = true
	first := m.files == nil
	m.mu.Unlock()
	root := a.cwd
	if root == "" {
		root = "."
	}
	go func() {
		opt := fsutil.ListOptions{}
		if first {
			opt.Progress = func(files []fsutil.Entry) {
				m.mu.Lock()
				m.files, m.gen = files, m.gen+1
				m.mu.Unlock()
				a.requestRender()
			}
		}
		files, _ := fsutil.ListFiles(context.Background(), root, opt)
		m.mu.Lock()
		m.files, m.gen, m.walking, m.walked = files, m.gen+1, false, time.Now()
		m.mu.Unlock()
		a.requestRender()
	}()
}

func (a *App) requestRender() {
	if a.ui != nil {
		a.ui.RequestRender()
	}
}

// rankFiles returns the best limit files for query, pi's way: by fuzzy
// score against the base name (the whole path once the query has a "/"),
// then shallower first, then shorter, then by name.
func rankFiles(files []fsutil.Entry, query string, limit int) []fsutil.Entry {
	type ranked struct {
		e     fsutil.Entry
		score float64
		depth int
	}
	less := func(x, y ranked) bool {
		if x.score != y.score {
			return x.score < y.score
		}
		if x.depth != y.depth {
			return x.depth < y.depth
		}
		if len(x.e.Path) != len(y.e.Path) {
			return len(x.e.Path) < len(y.e.Path)
		}
		return x.e.Path < y.e.Path
	}
	full := strings.Contains(query, "/")
	var top []ranked // the best so far, in order; limit is small
	for _, e := range files {
		text := e.Path
		if !full {
			text = path.Base(e.Path)
		}
		ok, score := tui.FuzzyMatch(query, text)
		if !ok {
			continue
		}
		r := ranked{e, score, strings.Count(e.Path, "/") + 1}
		if len(top) == limit && !less(r, top[limit-1]) {
			continue
		}
		i := len(top)
		for i > 0 && less(r, top[i-1]) {
			i--
		}
		if len(top) < limit {
			top = append(top, ranked{})
		}
		copy(top[i+1:], top[i:])
		top[i] = r
	}
	out := make([]fsutil.Entry, len(top))
	for i, r := range top {
		out[i] = r.e
	}
	return out
}

// mentionItems makes list rows of files: the name (a folder's ends in /)
// and the path, and the label width that lines the paths up.
func mentionItems(files []fsutil.Entry) ([]tui.SelectItem, int) {
	items := make([]tui.SelectItem, 0, len(files))
	width := 0
	for _, f := range files {
		label := path.Base(f.Path)
		if f.Dir {
			label += "/"
		}
		width = max(width, tui.VisibleWidth(label))
		items = append(items, tui.SelectItem{Label: label, Detail: f.Path, Value: f.Path, Data: f})
	}
	return items, min(width, 24)
}

// mentionKey handles the keys of an open "@" list: up/down move, tab and
// enter insert the selected path, esc closes the list.
func (a *App) mentionKey(key string) bool {
	l := a.mentionList()
	it, ok := l.Current()
	if !ok {
		if key == "escape" && a.mention.open && a.mentionSearching() {
			a.mention.dismissed = a.editor.Text()
			return true
		}
		return false
	}
	switch key {
	case "up":
		l.Move(-1)
	case "down":
		l.Move(1)
	case "tab", "enter":
		a.acceptMention(it.Data.(fsutil.Entry))
	case "escape":
		a.mention.dismissed = a.editor.Text()
	default:
		return false
	}
	return true
}

// acceptMention replaces the token before the cursor with f's path. A
// folder keeps the list open on its contents; a file gets a space after it.
func (a *App) acceptMention(f fsutil.Entry) {
	prefix := a.mention.prefix
	_, quoted := tui.ParseMention(prefix)
	p := f.Path
	if f.Dir {
		p += "/"
	}
	del, delAfter, insert, cursor := tui.CompleteMention(prefix, a.editor.AfterCursor(), tui.MentionValue(p, quoted), f.Dir)
	a.editor.Replace(del, delAfter, insert, cursor)
}

// mentionSearching reports that the first walk is still finding files.
func (a *App) mentionSearching() bool {
	m := &a.mention
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.walking && len(m.files) == 0
}

func (a *App) renderMentions(width int) []string {
	l := a.mentionList()
	if !a.mention.open {
		return nil
	}
	if len(l.Items) == 0 {
		if a.mentionSearching() {
			return []string{tui.Dim("   Searching…")}
		}
		return nil
	}
	l.Visible() // settles the selection before it goes in the key
	key := a.mention.ranked
	key.sel = l.Selected
	return a.mention.cache.Render(width, key, func() []string { return l.Render(width) })
}
