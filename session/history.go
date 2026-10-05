package session

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/sebastianrcnt/atto/config"
)

// Find locates a session file by ID in the active and archived directories.
func Find(id string) (string, error) {
	var found string
	for _, root := range []string{config.SessionsDir(), config.ArchivedDir()} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, "-"+id+".jsonl") {
				found = path
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found, nil
		}
	}
	return "", fmt.Errorf("session %s not found", id)
}

// Item is one searchable unit of a session: a message or compaction
// notes. N is its 1-based position among the session's entries, stable as
// the file grows.
type Item struct {
	N     int
	Label string // e.g. "user", "assistant", "tool: Run tests", "compaction"
	Text  string
}

// Items flattens entries into searchable text. Assistant tool calls are
// rendered as "$ command" lines so commands can be found too.
func Items(entries []Entry) []Item {
	var out []Item
	calls := map[string]string{} // tool call ID -> description
	for i, e := range entries {
		n := i + 1
		switch e.Type {
		case TypeCompaction:
			out = append(out, Item{N: n, Label: "compaction notes", Text: e.Notes})
		case TypeMessage:
			m := e.Message
			if m == nil {
				continue
			}
			switch m.Role {
			case "user":
				out = append(out, Item{N: n, Label: "user", Text: m.Content})
			case "assistant":
				var b strings.Builder
				if m.ReasoningContent != "" {
					b.WriteString("[thinking]\n" + strings.TrimSpace(m.ReasoningContent) + "\n")
				}
				if m.Content != "" {
					b.WriteString(strings.TrimSpace(m.Content) + "\n")
				}
				for _, tc := range m.ToolCalls {
					desc, cmd := toolCallText(tc.Function.Arguments)
					calls[tc.ID] = desc
					fmt.Fprintf(&b, "[%s] $ %s\n", desc, cmd)
				}
				out = append(out, Item{N: n, Label: "assistant", Text: strings.TrimSpace(b.String())})
			case "tool":
				label := "tool output"
				if d := calls[m.ToolCallID]; d != "" {
					label += ": " + d
				}
				out = append(out, Item{N: n, Label: label, Text: m.Content})
			}
		}
	}
	return out
}

// toolCallText pulls description and command out of bash arguments without
// importing the agent package.
func toolCallText(args string) (desc, cmd string) {
	var a struct {
		Description string `json:"description"`
		Command     string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "tool call", args
	}
	return a.Description, a.Command
}
