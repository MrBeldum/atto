package transcript

import (
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/session"
)

func TestBranchSummaryItems(t *testing.T) {
	var live Builder
	live.Event(agent.BranchSummaryStart{})
	live.Event(agent.BranchSummaryDelta{Text: "tried "})
	live.Event(agent.BranchSummaryDelta{Text: "X"})
	live.Event(agent.BranchSummaryEnd{Summary: "tried X", Elapsed: 1500 * time.Millisecond})
	replayed := FromEntries("", []session.Entry{{Type: session.TypeBranchSummary, Summary: "tried X", ElapsedMs: 1500}})
	got := live.Items()
	if len(got) != 1 || len(replayed) != 1 {
		t.Fatalf("live %+v, replayed %+v", got, replayed)
	}
	for _, it := range []Item{got[0], replayed[0]} {
		if it.Kind != BranchSummary || it.Text != "tried X" || it.Status != Completed || it.Duration != 1500*time.Millisecond {
			t.Fatalf("item %+v", it)
		}
	}

	// Interrupted while it is written, the item fails.
	var b Builder
	b.Event(agent.BranchSummaryStart{})
	b.Event(agent.BranchSummaryDelta{Text: "tri"})
	b.End()
	if it := b.Items()[0]; it.Status != Failed {
		t.Fatalf("canceled summary %+v", it)
	}
}
