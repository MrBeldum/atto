package app

import "testing"

// Shift+Left takes back the last steer the turn has not taken yet; once
// delivered, it stays.
func TestEditLastSteer(t *testing.T) {
	a := treeApp(t)
	a.steer("first")
	a.steer("why so slow?")
	a.editor.SetText("draft")
	a.onInput("\x1b[1;2D") // shift+left
	if a.editor.Text() != "why so slow?\ndraft" || len(a.pendingSteers) != 1 || a.pendingSteers[0] != "first" {
		t.Fatalf("editor %q, pending %q", a.editor.Text(), a.pendingSteers)
	}
	if got := a.agent.DrainSteers(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("agent steers %q", got)
	}
	// "first" was delivered (drained): it is not taken back.
	a.editor.SetText("")
	if a.editLastSteer() || a.editor.Text() != "" {
		t.Fatalf("a delivered steer came back: %q", a.editor.Text())
	}
}
