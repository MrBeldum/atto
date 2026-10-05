package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
)

// Continue answers a conversation that ends in the user's message, without
// adding one.
func TestContinueAnswersWithoutNewMessage(t *testing.T) {
	srv, seen := fakeServer(t, text("done"))
	a := newTestAgent(srv.URL)
	a.Restore([]session.Entry{{Type: session.TypeMessage, Message: &provider.Message{Role: "user", Content: "go"}}})
	var recorded []session.Entry
	a.Record = func(e session.Entry) { recorded = append(recorded, e) }
	if err := a.Continue(context.Background(), func(any) {}); err != nil {
		t.Fatal(err)
	}
	reqs := seen()
	if len(reqs) != 1 || len(reqs[0]) != 2 || reqs[0][1]["content"] != "go" {
		t.Fatalf("requests %v", reqs)
	}
	if len(recorded) != 1 || recorded[0].Message.Role != "assistant" {
		t.Fatalf("recorded %+v", recorded)
	}
}

// An interrupted model call keeps its streamed text, unless DiscardPartial.
func TestDiscardPartial(t *testing.T) {
	for _, discard := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"half an ans\"}}]}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		a := newTestAgent(srv.URL)
		a.DiscardPartial.Store(discard)
		var recorded []session.Entry
		a.Record = func(e session.Entry) { recorded = append(recorded, e) }
		ctx, cancel := context.WithCancel(context.Background())
		err := a.Run(ctx, "go", func(ev any) {
			if _, ok := ev.(TextDelta); ok {
				cancel()
			}
		})
		srv.Close()
		if err == nil {
			t.Fatal("interrupted")
		}
		want := 2 // the user's message and the partial answer
		if discard {
			want = 1
		}
		if len(recorded) != want {
			t.Fatalf("discard=%v: recorded %d entries, want %d", discard, len(recorded), want)
		}
	}
}
