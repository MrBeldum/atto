package tui

import (
	"slices"
	"strings"
	"testing"
)

func plain(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = StripEscapes(l)
	}
	return out
}

func TestMarkdownBlocks(t *testing.T) {
	src := "# Title\n\nSome **bold** and `code` text\nwrapped softly.\n\n- one\n- two\n  - nested\n1. first\n\n```go\nfunc main() {\n\tx := 1\n}\n```\n\n> quoted\n\n---\n\n| a | b |\n|---|--:|\n| 1 | 22 |"
	got := plain(Markdown(src, 30))
	want := []string{
		"Title",
		"",
		"Some bold and code text",
		"wrapped softly.",
		"",
		"• one",
		"• two",
		"  ◦ nested",
		"1. first",
		"",
		"╭ go",
		"│ func main() {",
		"│     x := 1",
		"│ }",
		"",
		"│ quoted",
		"",
		strings.Repeat("─", 30),
		"",
		"┌───┬────┐",
		"│ a │  b │",
		"├───┼────┤",
		"│ 1 │ 22 │",
		"└───┴────┘",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMarkdownInline(t *testing.T) {
	cases := map[string]string{
		"**b** *i* ***bi*** ~~s~~": "\x1b[1mb\x1b[22m \x1b[3mi\x1b[23m \x1b[1m\x1b[3mbi\x1b[23m\x1b[22m \x1b[9ms\x1b[29m",
		"snake_case_name":          "snake_case_name",
		"2 * 3 * 4":                "2 * 3 * 4",
		"**unclosed":               "**unclosed",
		`\*lit\*`:                  "*lit*",
		"`a*b*c`":                  codeStyleOn + "a*b*c" + codeStyleOff,
	}
	for in, want := range cases {
		if got := inline(in); got != want {
			t.Errorf("inline(%q) = %q, want %q", in, got, want)
		}
	}
	if got := StripEscapes(inline("[docs](https://x.y)")); got != "docs" {
		t.Errorf("link text %q", got)
	}
}

func TestMarkdownWidthAndStreaming(t *testing.T) {
	src := "| col | another long column |\n|---|---|\n| some long cell text here | x |\n\n```\nunterminated code that is long enough to wrap around"
	for _, w := range []int{12, 20, 40} {
		for _, l := range Markdown(src, w) {
			if VisibleWidth(l) > w {
				t.Errorf("width %d: line %q is %d wide", w, StripEscapes(l), VisibleWidth(l))
			}
		}
	}
}
