package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
	"github.com/sebastianrcnt/atto/tui"
)

// usageStats tracks token usage for the status line and /context.
type usageStats struct {
	last                  provider.Usage
	input, cached, output int // session totals
}

func (u *usageStats) add(x provider.Usage) {
	u.last = x
	u.input += x.PromptTokens
	u.cached += x.CachedTokens
	u.output += x.CompletionTokens
}

// fromEntries rebuilds totals from a resumed session.
func (u *usageStats) fromEntries(entries []session.Entry) {
	*u = usageStats{}
	for _, e := range entries {
		if e.Usage != nil {
			u.add(*e.Usage)
		}
	}
}

func pct(part, whole int) int {
	if whole <= 0 {
		return 0
	}
	return part * 100 / whole
}

// cacheLabel is "cache 93%" for the last request, or "" before any.
func (u *usageStats) cacheLabel() string {
	if u.last.PromptTokens == 0 {
		return ""
	}
	return fmt.Sprintf("cache %d%%", pct(u.last.CachedTokens, u.last.PromptTokens))
}

// contextBlock is the /context report.
type contextBlock struct{ lines []string }

func (c *contextBlock) Render(width int) []string {
	out := make([]string, len(c.lines))
	for i, l := range c.lines {
		out[i] = tui.Truncate("  "+l, width, "…")
	}
	return out
}

func (a *App) cmdContext(arg string) {
	if arg == "system" {
		a.add(&noticeBlock{text: "System prompt:\n\n" + a.agent.SystemPrompt(), style: tui.Dim})
		return
	}
	m := a.model()
	var lines []string
	head := fmt.Sprintf("%s · %s tokens", tui.Bold("Context"), fmtTokens(a.ctxTokens))
	if cw := m.Model.ContextWindow; cw > 0 {
		head += fmt.Sprintf(" of %s (%d%%)", fmtTokens(cw), pct(a.ctxTokens, cw))
		if limit := agent.AutoCompactLimit(m.Model); limit > 0 {
			head += tui.Dim(" · auto-compacts at " + fmtTokens(limit))
		}
	}
	lines = append(lines, head)

	if a.busy {
		lines = append(lines, tui.Dim("Breakdown is available when the turn finishes."))
	} else {
		b := a.agent.Breakdown()
		total := max(b.Total(), 1)
		rows := []struct {
			name  string
			chars int
		}{
			{"system prompt", b.System}, {"tool schema", b.Tools}, {"user messages", b.User}, {"images", b.Images},
			{"handoff notes", b.Notes}, {"assistant text", b.Assistant}, {"reasoning", b.Reasoning},
			{"tool calls", b.ToolCalls}, {"tool results", b.ToolResults},
		}
		for _, r := range rows {
			if r.chars == 0 {
				continue
			}
			p := pct(r.chars, total)
			lines = append(lines, fmt.Sprintf("  %-15s %s %6s %3d%%", r.name, contextBar(p, 20), "~"+fmtTokens(r.chars/4), p))
		}
		lines = append(lines, tui.Dim(fmt.Sprintf("  %d messages · sizes estimated at 4 characters per token", b.Messages)))
	}

	u := a.usage
	if u.last.PromptTokens > 0 {
		lines = append(lines, "", fmt.Sprintf("Last request   %s input · %s cached (%d%%) · %s output",
			fmtTokens(u.last.PromptTokens), fmtTokens(u.last.CachedTokens), pct(u.last.CachedTokens, u.last.PromptTokens), fmtTokens(u.last.CompletionTokens)))
		lines = append(lines, fmt.Sprintf("This session   %s input · %s cached (%d%%) · %s output",
			fmtTokens(u.input), fmtTokens(u.cached), pct(u.cached, u.input), fmtTokens(u.output)))
	}
	lines = append(lines, "", tui.Dim("/context system shows the system prompt · /request saves the raw last request"))
	a.add(&contextBlock{lines: lines})
}

// cmdRequest saves the last request body sent to the model, pretty-printed.
func (a *App) cmdRequest(string) {
	body := a.agent.LastRequest()
	if body == nil {
		a.notice("No request has been sent yet.")
		return
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") != nil {
		pretty.Write(body)
	}
	path := filepath.Join(config.Dir(), "cache", "last-request.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		a.errorNotice(err)
		return
	}
	if err := os.WriteFile(path, pretty.Bytes(), 0o600); err != nil {
		a.errorNotice(err)
		return
	}
	var req struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
	}
	_ = json.Unmarshal(body, &req)
	a.notice("Saved the last request (%d messages, %d tools, %s) to %s",
		len(req.Messages), len(req.Tools), fmtBytes(int64(len(body))), shortPath(path))
}
