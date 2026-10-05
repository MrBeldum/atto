package tui

import (
	"fmt"
	"slices"
	"testing"
)

// block is n numbered lines of a named block.
func block(name string, n int) *lines {
	c := &lines{}
	for i := 1; i <= n; i++ {
		c.l = append(c.l, fmt.Sprintf("%s%d", name, i))
	}
	return c
}

// A block above the view, or at its top, changing height does not move
// the view while scrolled up; streaming below still keeps it put.
func TestScrollAnchorSurvivesHeightChanges(t *testing.T) {
	v := newVterm(20, 6)
	ui := New(v)
	a, b, c := block("a", 8), block("b", 8), block("c", 8)
	ui.Body.Add(a, b, c)
	ui.Footer.Add(&lines{l: []string{"> in", "status"}})
	ui.RenderNow()
	ui.ScrollBy(10)
	ui.RenderNow()
	top := func() string { return noBar(v.screenRows())[0] }
	before := slices.Clone(noBar(v.screenRows()))
	if top() != "b3" {
		t.Fatalf("setup: %q", before)
	}

	a.l = append(a.l, "a9", "a10", "a11") // grows above the view
	ui.RenderNow()
	if got := noBar(v.screenRows()); !slices.Equal(got, before) {
		t.Fatalf("a block above grew: %q, was %q", got, before)
	}
	a.l = a.l[:3] // and shrinks
	ui.RenderNow()
	if got := noBar(v.screenRows()); !slices.Equal(got, before) {
		t.Fatalf("a block above shrank: %q, was %q", got, before)
	}

	c.l = append(c.l, "c9", "c10") // below: streaming
	ui.RenderNow()
	if got := noBar(v.screenRows()); !slices.Equal(got, before) {
		t.Fatalf("a block below grew: %q, was %q", got, before)
	}
	if !ui.NewBelow() {
		t.Fatal("output below is flagged")
	}

	// The block at the top of the view changing keeps its first visible
	// line when it still has it.
	b.l = append([]string{"b0"}, b.l...) // a line before the top one, inside it
	ui.RenderNow()
	if got := top(); got != "b2" {
		t.Fatalf("the top block changed: top %q", got)
	}

	// A scroll between frames is kept.
	ui.ScrollBy(1)
	a.l = append(a.l, "x")
	ui.RenderNow()
	if got := top(); got != "b1" {
		t.Fatalf("scroll with a change: top %q", got)
	}
}
