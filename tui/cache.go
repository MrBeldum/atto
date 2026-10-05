package tui

// RenderCache keeps a component's last rendered lines, so a frame in which
// the component did not change costs it nothing.
//
// The key is a comparable value made, at render time, of everything the
// lines depend on (besides the width): typically a struct of the fields
// Render reads, with a strings.Builder's text as its String(), which does
// not copy. Building the key from the state, rather than bumping a version
// on each change, means no mutation can forget to invalidate. Lines that
// depend on the clock cannot be cached; render those on every call.
//
// Lines from the cache are shared: callers must not modify them.
type RenderCache[K comparable] struct {
	ok    bool
	width int
	key   K
	lines []string
}

// Get returns the cached lines if they were rendered for width and key.
func (c *RenderCache[K]) Get(width int, key K) ([]string, bool) {
	if c.ok && c.width == width && c.key == key {
		return c.lines, true
	}
	return nil, false
}

// Put caches lines for width and key and returns them.
func (c *RenderCache[K]) Put(width int, key K, lines []string) []string {
	c.ok, c.width, c.key, c.lines = true, width, key, lines
	return lines
}

// Render returns the cached lines for width and key, calling render to
// make them when they are not cached.
func (c *RenderCache[K]) Render(width int, key K, render func() []string) []string {
	if lines, ok := c.Get(width, key); ok {
		return lines
	}
	return c.Put(width, key, render())
}

// lineMemo applies a function of one line to each line of a frame,
// reusing the previous frame's result for each line that is the same as
// the one in its position then. Most lines of a frame are the lines of the
// last one, and comparing them costs far less than making new strings.
type lineMemo struct {
	key     int
	in, out []string
}

// apply returns f of every line, in a new slice. key is any other input of
// f (a width): a different key forgets the previous frame.
func (m *lineMemo) apply(lines []string, key int, f func(string) string) []string {
	if key != m.key {
		m.in, m.out, m.key = m.in[:0], m.out[:0], key
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if i < len(m.in) && m.in[i] == l {
			out[i] = m.out[i]
		} else {
			out[i] = f(l)
		}
	}
	// Private copies: the caller may modify both slices.
	m.in, m.out = append(m.in[:0], lines...), append(m.out[:0], out...)
	return out
}
