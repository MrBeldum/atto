package tui

// Scroll anchoring. While the user is scrolled up reading, the first line in
// view must stay put when the body changes height: a block below grows
// (streaming), but also a block above or at the top of the view (an
// extension replacing a block's text, a click that collapses one). The view
// is anchored to a line of a component: which one, and how far into it.

// anchorState is what the last fullscreen frame says about the view.
type anchorState struct {
	scroll int // t.scroll as that frame left it
}

type anchorLine struct {
	child  Component
	offset int // lines into the child
}

// anchorBefore finds the component that holds the first line in view, from
// the layout of the last frame, and how far into it that line is. The user
// may have scrolled since: the line is the one the view shows now.
func (t *TUI) anchorBefore() (anchorLine, bool) {
	if t.scroll == 0 || t.viewRows == 0 {
		return anchorLine{}, false
	}
	line := t.viewStart - (t.scroll - t.anchor.scroll)
	for _, r := range t.Body.ranges {
		if line >= r.start && line < r.end {
			return anchorLine{r.c, line - r.start}, true
		}
	}
	return anchorLine{}, false
}

// anchorScroll is the scroll that keeps a in view at the top, after the
// body was rendered again to bodyLen lines; false when the component is
// gone.
func (t *TUI) anchorScroll(a anchorLine, bodyLen int) (int, bool) {
	for _, r := range t.Body.ranges {
		if !sameComponent(r.c, a.child) {
			continue
		}
		start := r.start + min(a.offset, max(0, r.end-r.start-1))
		return max(0, bodyLen-start-t.viewRows), true
	}
	return 0, false
}

// sameComponent compares components that may not be comparable.
func sameComponent(a, b Component) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}
