package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/config"
)

// After a model switch, the next user message says so, once.
func TestModelChangeNote(t *testing.T) {
	srv, seen := fakeServer(t, text("a"), text("b"), text("c"))
	a := newTestAgent(srv.URL)
	run := func(in string) {
		if err := a.Run(context.Background(), in, func(any) {}); err != nil {
			t.Fatal(err)
		}
	}
	lastUser := func(i int) string {
		msgs := seen()[i]
		return msgs[len(msgs)-1]["content"].(string)
	}
	run("one")
	if u := lastUser(0); u != "one" {
		t.Fatalf("first request: %q", u)
	}
	a.SetModel(config.ModelRef{ProviderName: "t", Provider: config.Provider{BaseURL: srv.URL}, Model: config.Model{ID: "m2"}})
	run("two")
	if u := lastUser(1); !strings.HasPrefix(u, "two\n\n[atto] The model changed from t/m to t/m2.") {
		t.Fatalf("after switch: %q", u)
	}
	run("three")
	if u := lastUser(2); u != "three" {
		t.Fatalf("note repeated: %q", u)
	}
}
