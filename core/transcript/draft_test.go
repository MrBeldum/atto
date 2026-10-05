package transcript

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/session"
)

// argChunk is a piece of the arguments of tool call i, streamed after the
// call started.
func argChunk(i int, args string) string {
	return chunk(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": args}}}}, "")
}

// itemLog records what the handler is told, as short strings.
func itemLog(b *Builder, log *[]string) {
	show := func(what string, it *Item) {
		*log = append(*log, fmt.Sprintf("%s %s pending=%v %q %q", what, it.ID, it.Pending, it.Description, it.Command))
	}
	b.Handler = Handler{
		Started:   func(it *Item) { show("started", it) },
		Updated:   func(it *Item) { show("updated", it) },
		Completed: func(it *Item) { show("completed", it) },
	}
}

func TestPendingToolIsTheRunningTool(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	url := scripted(t,
		[]string{
			chunk(map[string]any{"content": "Writing."}, ""),
			chunk(map[string]any{"tool_calls": []any{call(0, "c1", `{"descr`)}}, ""),
			argChunk(0, `iption":"Say hi","comm`),
			argChunk(0, `and":"echo hi`),
			argChunk(0, `"}`),
			chunk(map[string]any{}, "tool_calls"),
		},
		say("done"),
	)
	ag := agent.New(config.ModelRef{ProviderName: "t", Provider: config.Provider{BaseURL: url}, Model: config.Model{ID: "m"}}, "", t.TempDir())
	w := session.New(t.TempDir())
	ag.Record, ag.EntryID = w.Append, w.Leaf

	var log []string
	live := Builder{IDPrefix: "x-i"}
	itemLog(&live, &log)
	live.Event(Input{Text: "go"})
	if err := ag.Run(context.Background(), "go", live.Event); err != nil {
		t.Fatal(err)
	}
	live.End()
	w.Close()

	// One block per call: it starts pending, takes the description and
	// command as they arrive, and runs and completes as the same item.
	var tool []string
	for _, l := range log {
		if strings.Contains(l, "x-i3") {
			tool = append(tool, l)
		}
	}
	want := []string{
		`started x-i3 pending=true "" ""`,
		`updated x-i3 pending=true "Say hi" ""`,
		`updated x-i3 pending=true "Say hi" "echo hi"`,
		`updated x-i3 pending=false "Say hi" "echo hi"`,
		`completed x-i3 pending=false "Say hi" "echo hi"`,
	}
	if !reflect.DeepEqual(tool, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(tool, "\n"), strings.Join(want, "\n"))
	}
	n := 0
	for _, it := range live.Items() {
		if it.Kind == Tool {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d tool items", n)
	}

	// Pending is live only: the resumed conversation is the same.
	_, entries, err := session.Load(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	replayed := FromEntries("x-i", session.Active(entries))
	if got := live.Items(); !reflect.DeepEqual(got, replayed) {
		t.Fatalf("live %+v\nreplay %+v", got, replayed)
	}
}

func TestPendingToolEndsWithoutRunning(t *testing.T) {
	var log []string
	var b Builder
	itemLog(&b, &log)
	b.Event(agent.ToolDraft{Index: 0})
	b.Event(agent.ToolDraft{Index: 1, Args: agent.BashArgs{Command: "ls"}})
	b.Event(agent.ToolDraft{Index: 2})
	b.Event(agent.ToolDraftEnd{Index: 0, Err: "unknown tool"})
	b.Event(agent.ToolStart{ID: "c2", Index: 1, Args: agent.BashArgs{Description: "ls", Command: "ls"}})
	b.End() // the response ended before call 2 was complete

	items := b.Items()
	if len(items) != 3 {
		t.Fatalf("items %+v", items)
	}
	for i, it := range items {
		if it.Pending {
			t.Errorf("item %d still pending", i)
		}
	}
	if r := items[0].Result; items[0].Status != Failed || r == nil || r.Err != "unknown tool" {
		t.Errorf("rejected call: %+v %+v", items[0], items[0].Result)
	}
	// c2 was started and never ended: interrupted, as on any aborted turn.
	if r := items[1].Result; items[1].Status != Failed || items[1].CallID != "c2" || r == nil || !r.Canceled {
		t.Errorf("running call: %+v %+v", items[1], items[1].Result)
	}
	if r := items[2].Result; items[2].Status != Failed || r == nil || !r.Canceled {
		t.Errorf("unfinished call: %+v %+v", items[2], items[2].Result)
	}
	completed := 0
	for _, l := range log {
		if strings.HasPrefix(l, "completed") {
			completed++
		}
	}
	if completed != 3 {
		t.Fatalf("each item completes once: %v", log)
	}

	// Nothing is left over for the next turn: a call with the same index
	// is a new item.
	b.Event(agent.ToolStart{ID: "c9", Index: 0, Args: agent.BashArgs{Command: "pwd"}})
	if items := b.Items(); len(items) != 4 || items[3].CallID != "c9" || items[3].Pending {
		t.Fatalf("items %+v", items)
	}
}
