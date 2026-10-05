package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/agent"
)

// A call the model streams is announced once, with its final arguments;
// a call that never runs is not announced at all.
func TestPrinterStreamedToolCall(t *testing.T) {
	for _, format := range []string{"stream-json", "text"} {
		var out, errOut bytes.Buffer
		p := &printer{format: format, verbose: true, out: &out, errOut: &errOut, res: &printResult{}}
		printDraftedCall(t, p)
		if format == "text" {
			if e := errOut.String(); e != "\n● List  $ ls\n  └ exit 0 · 1ms\n" || out.Len() != 0 {
				t.Fatalf("stdout %q stderr %q", out.String(), e)
			}
			continue
		}
		want := `{"command":"ls","description":"List","id":"c1","type":"tool_use"}
{"description":"List","duration_ms":1,"exit_code":0,"id":"c1","output":"a","timed_out":false,"type":"tool_result"}
`
		if out.String() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
		}
	}
}

func printDraftedCall(t *testing.T, p *printer) {
	t.Helper()
	for _, ev := range []any{
		agent.ToolDraft{Index: 0},
		agent.ToolDraft{Index: 1},
		agent.ToolDraft{Index: 0, Args: agent.BashArgs{Description: "List", Command: "l"}},
		agent.ToolDraft{Index: 0, Args: agent.BashArgs{Description: "List", Command: "ls"}},
		agent.ToolDraftEnd{Index: 1, Err: "command is empty"},
		agent.StepEnd{},
		agent.ToolStart{ID: "c1", Index: 0, Args: agent.BashArgs{Description: "List", Command: "ls"}},
		agent.ToolEnd{ID: "c1", Result: agent.BashResult{Output: "a\n", Duration: time.Millisecond}, Text: "a"},
	} {
		p.event(ev)
	}
	p.tr.End()
}
