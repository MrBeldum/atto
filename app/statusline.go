package app

import (
	"bytes"
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
	var out []string
	if a.statusCmd {
		for _, l := range a.statusLines {
			out = append(out, tui.Truncate(" "+l, width, "…"))
		}
	} else {
		out = append(out, a.builtinStatus(width))
	}
	// Transient indicators go on their own line so custom output is untouched.
	var flags []string
	if off := a.ui.ScrollOffset(); off > 0 {
		flags = append(flags, tui.FG(3, fmt.Sprintf("↓ %d more lines (scroll or PgDn)", off)))
	}
	if p := a.goalPill(); p != "" {
		flags = append(flags, p)
	}
	if a.jobCount > 0 {
		flags = append(flags, tui.FG(2, fmt.Sprintf("● %d job%s running (/jobs)", a.jobCount, plural(a.jobCount))))
	}
	if a.timerCount > 0 {
		flags = append(flags, tui.FG(4, fmt.Sprintf("⏱ %d timer%s (/timers)", a.timerCount, plural(a.timerCount))))
	}
	if len(flags) > 0 {
		out = append(out, tui.Truncate(" "+strings.Join(flags, tui.Dim(" · ")), width, "…"))
	}
	return out
}

func contextBar(pct, cells int) string {
	filled := min(cells, (pct*cells+50)/100)
	return tui.FG(6, strings.Repeat("━", filled)) + strings.Repeat("─", cells-filled)
}

func (a *App) builtinStatus(width int) string {
	m, effort := a.agent.Current()
	sep := tui.Dim(" · ")

	left := tui.FG(6, "◆ ") + m.Model.DisplayName()
	if effort != "" && len(m.Model.Levels()) > 0 {
		left += sep + effortStyle(effort)
	}
	if cw := m.Model.ContextWindow; cw > 0 {
		pct := a.ctxTokens * 100 / cw
		bar := contextBar(pct, 10) + fmt.Sprintf(" %d%% %s/%s", pct, tui.FormatTokens(a.ctxTokens), tui.FormatTokens(cw))
		if limit := agent.AutoCompactLimit(m.Model); limit > 0 && a.ctxTokens*100/limit >= 80 {
			bar = tui.FG(3, bar) // nearing auto-compaction
		} else {
			bar = tui.Dim(bar)
		}
		left += "  " + bar
	}
	if c := a.usage.cacheLabel(); c != "" {
		left += sep + tui.Dim(c)
	}

	lw := tui.VisibleWidth(left) + 1
	mem := tui.Dim(fmtBytes(rssBytes.Load()))
	name := ""
	if a.sessName != "" {
		name = tui.FG(5, a.sessName) + sep
	}
	// The path gets whatever room is left, shortened from the front.
	room := width - lw - 2 - tui.VisibleWidth(name) - tui.VisibleWidth(sep) - tui.VisibleWidth(mem)
	branch := ""
	if a.gitBranch != "" {
		branch = " (" + a.gitBranch + ")"
	}
	where := compressPath(shortPath(a.cwd), room-tui.VisibleWidth(branch)) + branch
	r := name + mem
	if room >= 8 {
		r = name + tui.Dim(where) + sep + mem
	}
	gap := width - lw - tui.VisibleWidth(r)
	if gap < 2 {
		return tui.Truncate(" "+left, width, "…")
	}
	return " " + left + strings.Repeat(" ", gap) + r
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
