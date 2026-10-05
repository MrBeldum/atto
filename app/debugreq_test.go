package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrefixNote(t *testing.T) {
	m := func(s ...string) []json.RawMessage {
		var out []json.RawMessage
		for _, x := range s {
			out = append(out, json.RawMessage(x))
		}
		return out
	}
	sys, u, a := `{"role":"system","content":"s"}`, `{"role":"user","content":"u"}`, `{"content":"a","role":"assistant"}`
	for _, c := range []struct {
		prev, cur []json.RawMessage
		want      string
	}{
		{m(sys, u), m(sys, u, `{"role":"assistant",  "content":"a"}`, u), "continues the previous request (+2 messages)"},
		{m(sys, u, a), m(`{"role":"system","content":"s2"}`, u, a, u), "differs from the previous request at message 0"},
		{m(sys, u, a), m(sys, u), "shorter than the previous request"},
	} {
		if got := prefixNote(c.prev, c.cur); !strings.HasPrefix(got, c.want) {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
