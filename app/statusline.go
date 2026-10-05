package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/shell"
	"github.com/sebastianrcnt/atto/tui"
)

// statusInput is the JSON a statusLine command receives on stdin. Field
// names follow Claude Code's status line input where one exists.
type statusInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	SessionName    string `json:"session_name,omitempty"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	Version        string `json:"version"`
	Model          struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Provider    string `json:"provider"`
	} `json:"model"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	ContextWindow struct {
		UsedTokens       int `json:"used_tokens"`
		Size             int `json:"context_window_size"`
		UsedPercentage   int `json:"used_percentage"`
		AutoCompactLimit int `json:"auto_compact_limit"`
	} `json:"context_window"`
	Effort    string `json:"effort"`
	GitBranch string `json:"git_branch,omitempty"`
	Busy      bool   `json:"busy"`
	Memory    struct {
		RSSBytes int64 `json:"rss_bytes"`
	} `json:"memory"`
	Cache struct {
		LastInputTokens  int `json:"last_input_tokens"`
		LastCachedTokens int `json:"last_cached_tokens"`
		InputTokens      int `json:"input_tokens"`  // session total
		CachedTokens     int `json:"cached_tokens"` // session total
		OutputTokens     int `json:"output_tokens"` // session total
	} `json:"cache"`
}

func (a *App) statusInput() statusInput {
	m, effort := a.agent.Current()
	var in statusInput
	in.HookEventName = "Status"
	in.SessionID = a.sess.ID
	in.SessionName = a.sessName
	in.TranscriptPath = a.sess.Path
	in.Cwd = a.cwd
	in.Version = Version
	in.Model.ID = m.Model.ID
	in.Model.DisplayName = m.Model.DisplayName()
	in.Model.Provider = m.ProviderName
	in.Workspace.CurrentDir = a.cwd
	in.Workspace.ProjectDir = a.cwd
	in.ContextWindow.UsedTokens = a.ctxTokens
	in.ContextWindow.Size = m.Model.ContextWindow
	if m.Model.ContextWindow > 0 {
		in.ContextWindow.UsedPercentage = a.ctxTokens * 100 / m.Model.ContextWindow
	}
	in.ContextWindow.AutoCompactLimit = agent.AutoCompactLimit(m.Model)
	in.Effort = effort
	in.GitBranch = a.gitBranch
	in.Busy = a.busy
	in.Memory.RSSBytes = rssBytes.Load()
	in.Cache.LastInputTokens = a.usage.last.PromptTokens
	in.Cache.LastCachedTokens = a.usage.last.CachedTokens
	in.Cache.InputTokens, in.Cache.CachedTokens, in.Cache.OutputTokens = a.usage.input, a.usage.cached, a.usage.output
	return in
}

// gitBranch finds the branch checked out in dir or a parent, reading
// .git/HEAD directly so it costs no process.
func gitBranch(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		gitPath := filepath.Join(d, ".git")
		if st, err := os.Stat(gitPath); err == nil {
			if !st.IsDir() { // worktree: "gitdir: <path>"
				b, err := os.ReadFile(gitPath)
				if err != nil {
					return ""
				}
				gitPath = strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
				if !filepath.IsAbs(gitPath) {
					gitPath = filepath.Join(d, gitPath)
				}
			}
			head, err := os.ReadFile(filepath.Join(gitPath, "HEAD"))
			if err != nil {
				return ""
			}
			h := strings.TrimSpace(string(head))
			if ref, ok := strings.CutPrefix(h, "ref: refs/heads/"); ok {
				return ref
			}
			if len(h) >= 7 {
				return h[:7] // detached
			}
			return ""
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

// startStatusLine runs background refreshes: memory and git branch every
// 2s, and the custom statusLine command when its input changes.
func (a *App) startStatusLine(cfg *config.StatusLine) {
	a.statusWake = make(chan struct{}, 1)
	startMemoryMonitor(2*time.Second, a.quit, func() {
		branch := gitBranch(a.cwd)
		a.ui.Do(func() { a.gitBranch = branch })
		a.statusTrigger()
	})
	if cfg == nil || cfg.Command == "" {
		return
	}
	go a.statusLoop(cfg)
}

// statusTrigger asks the custom status line to refresh (debounced).
func (a *App) statusTrigger() {
	if a.statusWake == nil {
		return
	}
	select {
	case a.statusWake <- struct{}{}:
	default:
	}
}

func (a *App) statusLoop(cfg *config.StatusLine) {
	var last []byte
	var refresh <-chan time.Time
	if cfg.RefreshInterval > 0 {
		t := time.NewTicker(time.Duration(cfg.RefreshInterval) * time.Second)
		defer t.Stop()
		refresh = t.C
	}
	for {
		forced := false
		select {
		case <-a.quit:
			return
		case <-a.statusWake:
		case <-refresh:
			forced = true
		}
		time.Sleep(300 * time.Millisecond) // debounce, as Claude Code does
		var in statusInput
		a.ui.Do(func() { in = a.statusInput() })
		input, _ := json.Marshal(in)
		if !forced && bytes.Equal(input, last) {
			continue
		}
		last = input
		lines, err := runStatusCommand(cfg.Command, input, a.cwd)
		a.ui.Do(func() {
			if err != nil {
				a.statusLines = []string{tui.FG(1, "statusLine: "+err.Error())}
			} else {
				a.statusLines = lines
			}
		})
	}
}

func runStatusCommand(command string, input []byte, cwd string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := shell.Command(ctx, command)
	cmd.Dir = cwd
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// renderStatus draws the custom status line if configured, otherwise the
// built-in one:
//
//	◆ Orca Local · medium  ━━─────── 12% 31k/262k · cache 93%    name · ~/proj (main) · 18MB
//
// Only characters with an unambiguous width (box drawing renders as one
// column everywhere the editor rules do) so CJK terminals line up.
func (a *App) renderStatus(width int) []string {
	// The goal indicator goes at the right end of the first row, as codex's
	// footer shows it; the row gives up room for it. On a terminal too narrow
	// for both, it takes a row of its own.
	ind := a.goalIndicator()
	rowWidth, own := width, false
	if ind != "" {
		if rowWidth = width - tui.VisibleWidth(ind) - 2; rowWidth < minStatusWithGoal {
			rowWidth, own = width, true
		}
	}
	var out []string
	if a.statusCmd {
		for i, l := range a.statusLines {
			w := width
			if i == 0 {
				w = rowWidth
			}
			out = append(out, tui.Truncate(" "+l, w, "…"))
		}
	} else {
		out = append(out, a.builtinStatus(rowWidth))
	}
	if len(out) > 0 {
		out[0] = a.withToast(out[0], rowWidth)
		if ind != "" && !own {
			out[0] += strings.Repeat(" ", max(1, width-1-tui.VisibleWidth(out[0])-tui.VisibleWidth(ind))) + ind
		}
	}
	// Transient indicators go on their own line so custom output is untouched.
	flags := a.extensionStatus()
	if ind != "" && own {
		flags = append(flags, ind)
	}
	if a.jobCount > 0 {
		flags = append(flags, tui.FG(2, fmt.Sprintf("● %d job%s running (/jobs)", a.jobCount, plural(a.jobCount))))
	}
	if a.timerCount > 0 {
		flags = append(flags, tui.FG(4, fmt.Sprintf("⏱ %d timer%s (/timers)", a.timerCount, plural(a.timerCount))))
	}
	if r := a.remoteStatus(); r != "" {
		flags = append(flags, r)
	}
	if len(flags) > 0 {
		out = append(out, tui.Truncate(" "+strings.Join(flags, tui.Dim(" · ")), width, "…"))
	}
	return out
}

// minStatusWithGoal is the room the status row needs to share it with the
// goal indicator (the model, the context bar).
const minStatusWithGoal = 24

func contextBar(pct, cells int) string {
	filled := min(cells, (pct*cells+50)/100)
	return tui.FG(6, strings.Repeat("━", filled)) + strings.Repeat("─", cells-filled)
}

// compactTokens is pi's footer notation: 950, 1.2k, 12k, 1.2M, 12M.
func compactTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10000:
		return strings.Replace(fmt.Sprintf("%.1fk", float64(n)/1e3), ".0k", "k", 1)
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n < 10000000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	return fmt.Sprintf("%dM", (n+500000)/1000000)
}

// priced reports whether the model has prices (local models have none).
func priced(m config.Model) bool {
	c := m.Cost
	return c != nil && (c.Input > 0 || c.Output > 0 || c.CacheRead > 0 || c.CacheWrite > 0)
}

// statusItem is one piece of the built-in status line. Items are dropped
// as the terminal narrows, lowest drop level first (see the drop levels
// below); level 0 is never dropped.
type statusItem struct {
	text  string
	pre   string // separator before it; a dot by default
	drop  int
	right bool
}

// Drop levels, least important first, as pi's footer gives way: memory,
// the session name, cache totals, cache hit rate, token totals, cost, the
// directory, the context size, the effort.
const (
	dropMem = iota + 1
	dropName
	dropCacheTotals
	dropCacheRate
	dropTokens
	dropCost
	dropPath
	dropCtxSize
	dropEffort
)

// minPath is the room the directory needs to be worth showing.
const minPath = 14

// builtinStatus is atto's default status line, with pi's footer items:
//
//	◆ model · effort  ━━─── 12% 31k/262k · cache 93% · ↑12k ↓3.4k · R80k W2k · $0.123   ~/proj (main) · 18MB
//
// The left side is the model and the usage, the right side where we are.
func (a *App) builtinStatus(width int) string {
	m, effort := a.agent.Current()
	sep := tui.Dim(" · ")
	u := &a.usage

	items := []statusItem{{text: tui.FG(6, "◆ ") + m.Model.DisplayName()}}
	if effort != "" && len(m.Model.Levels()) > 0 {
		items = append(items, statusItem{text: effortStyle(effort), drop: dropEffort})
	}
	if cw := m.Model.ContextWindow; cw > 0 {
		pct := a.ctxTokens * 100 / cw
		style := tui.Dim
		if limit := agent.AutoCompactLimit(m.Model); limit > 0 && a.ctxTokens*100/limit >= 80 {
			style = func(s string) string { return tui.FG(3, s) } // nearing auto-compaction
		}
		items = append(items,
			statusItem{text: style(contextBar(pct, 10) + fmt.Sprintf(" %d%%", pct)), pre: "  "},
			statusItem{text: style(fmt.Sprintf("%s/%s", tui.FormatTokens(a.ctxTokens), tui.FormatTokens(cw))), pre: " ", drop: dropCtxSize})
	}
	if c := u.cacheLabel(); c != "" {
		items = append(items, statusItem{text: tui.Dim(c), drop: dropCacheRate})
	}
	var io, rw []string
	if n := u.fresh(); n > 0 {
		io = append(io, "↑"+compactTokens(n))
	}
	if u.output > 0 {
		io = append(io, "↓"+compactTokens(u.output))
	}
	if u.cached > 0 {
		rw = append(rw, "R"+compactTokens(u.cached))
	}
	if u.cacheWrite > 0 {
		rw = append(rw, "W"+compactTokens(u.cacheWrite))
	}
	if len(io) > 0 {
		items = append(items, statusItem{text: tui.Dim(strings.Join(io, " ")), drop: dropTokens})
	}
	if len(rw) > 0 {
		items = append(items, statusItem{text: tui.Dim(strings.Join(rw, " ")), drop: dropCacheTotals})
	}
	// Only a model with prices has a cost; a local model's session is free.
	if priced(m.Model) || u.cost > 0 {
		items = append(items, statusItem{text: tui.Dim(fmt.Sprintf("$%.3f", u.cost)), drop: dropCost})
	}
	if a.sessName != "" {
		items = append(items, statusItem{text: tui.FG(5, a.sessName), drop: dropName, right: true})
	}
	items = append(items, statusItem{text: tui.Dim(fmtBytes(rssBytes.Load())), drop: dropMem, right: true})

	branch := ""
	if a.gitBranch != "" {
		branch = " (" + a.gitBranch + ")"
	}
	where := shortPath(a.cwd)

	join := func(cut int, right bool) string {
		var b strings.Builder
		for _, it := range items {
			if it.right != right || (it.drop != 0 && it.drop <= cut) {
				continue
			}
			if b.Len() > 0 {
				b.WriteString(cmp.Or(it.pre, sep))
			}
			b.WriteString(it.text)
		}
		return b.String()
	}

	var left, right string
	for cut := 0; cut <= dropEffort; cut++ {
		left, right = join(cut, false), join(cut, true)
		if cut < dropPath {
			// The directory takes what room remains, shortened from the front.
			room := width - 1 - tui.VisibleWidth(left) - 2
			if right != "" {
				room -= tui.VisibleWidth(sep) + tui.VisibleWidth(right)
			}
			if room >= min(minPath, tui.VisibleWidth(where+branch)) {
				p := tui.Dim(compressPath(where, room-tui.VisibleWidth(branch)) + branch)
				if right != "" {
					p += sep
				}
				right = p + right
			}
		}
		if 1+tui.VisibleWidth(left)+2+tui.VisibleWidth(right) <= width {
			break
		}
	}
	gap := width - 1 - tui.VisibleWidth(left) - tui.VisibleWidth(right)
	if gap < 2 {
		return tui.Truncate(" "+left, width, "…")
	}
	return " " + left + strings.Repeat(" ", gap) + right
}

// compressPath shortens p to at most w columns by dropping leading
// directories: "~/a/b/c/d" -> "…/c/d".
func compressPath(p string, w int) string {
	if tui.VisibleWidth(p) <= w {
		return p
	}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		c := "…/" + strings.Join(parts[i:], "/")
		if tui.VisibleWidth(c) <= w {
			return c
		}
	}
	return tui.Truncate(parts[len(parts)-1], max(1, w), "…")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
