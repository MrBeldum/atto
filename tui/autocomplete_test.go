package tui

import "testing"

func TestFuzzyMatch(t *testing.T) {
	for _, c := range []struct {
		q, text string
		ok      bool
	}{
		{"", "anything", true},
		{"mgo", "main.go", true},
		{"MAIN", "main.go", true},
		{"gom", "main.go", false},
		{"toolong", "short", false},
		{"v2", "2v.txt", true}, // letters and digits swapped
		{"2v", "v2.txt", true},
		{"ed", "에디터.go", false},
		{"디터", "에디터.go", true},
	} {
		if ok, _ := FuzzyMatch(c.q, c.text); ok != c.ok {
			t.Errorf("FuzzyMatch(%q, %q) = %v", c.q, c.text, ok)
		}
	}
	score := func(q, text string) float64 { _, s := FuzzyMatch(q, text); return s }
	if !(score("main.go", "main.go") < score("main", "main.go")) {
		t.Error("an exact match beats a prefix")
	}
	if !(score("edit", "editor.go") < score("edit", "xeyedxixt.go")) {
		t.Error("a consecutive run beats scattered letters")
	}
	if !(score("go", "x_go") < score("go", "xgo")) {
		t.Error("a word start beats the middle of a word")
	}
	if !(score("v2", "v2") < score("v2", "2v")) {
		t.Error("the swapped form scores worse")
	}
}

func TestMentionPrefix(t *testing.T) {
	for _, c := range []struct {
		text, want string
		ok         bool
	}{
		{"@", "@", true},
		{"@src/ma", "@src/ma", true},
		{"look at @app", "@app", true},
		{"tab\t@x", "@x", true},
		{"(@main", "@main", true}, // an opening wrapper
		{"`@main", "@main", true},
		{"a=@x", "@x", true},
		{"보세요，@파일", "@파일", true},    // after CJK punctuation
		{"user@example", "", false}, // inside a word
		{"email me@", "", false},
		{"@a b", "", false},
		{"@x ", "", false},
		{`@"my docs/no`, `@"my docs/no`, true},
		{`see @"a b`, `@"a b`, true},
		{`x@"a b`, "", false},
		{`@"a b" next`, "", false}, // the quote is closed
		{`@"a b" @`, "@", true},
		{"", "", false},
	} {
		got, ok := MentionPrefix(c.text)
		if got != c.want || ok != c.ok {
			t.Errorf("MentionPrefix(%q) = %q, %v; want %q, %v", c.text, got, ok, c.want, c.ok)
		}
	}
	if q, quoted := ParseMention(`@"a b/c`); q != "a b/c" || !quoted {
		t.Errorf("quoted: %q %v", q, quoted)
	}
	if q, quoted := ParseMention("@src/"); q != "src/" || quoted {
		t.Errorf("plain: %q %v", q, quoted)
	}
}

func TestCompleteMention(t *testing.T) {
	apply := func(text string, cursor int, prefix, path string, dir, quoted bool) (string, int) {
		e := NewEditor("")
		e.SetText(text)
		e.pos = cursor
		del, after, ins, cur := CompleteMention(prefix, e.AfterCursor(), MentionValue(path, quoted), dir)
		e.Replace(del, after, ins, cur)
		return e.Text(), e.Cursor()
	}
	for _, c := range []struct {
		text         string
		cursor       int
		prefix, path string
		dir, quoted  bool
		want         string
		wantCur      int
	}{
		{"see @ma", 7, "@ma", "app/main.go", false, false, "see @app/main.go ", 17},
		{"@a", 2, "@a", "app/", true, false, "@app/", 5},
		{"@my", 3, "@my", "my docs/", true, false, `@"my docs/"`, 10}, // cursor before the closing quote
		{"@my", 3, "@my", "my docs/a.txt", false, false, `@"my docs/a.txt" `, 17},
		{`@"my docs/"`, 10, `@"my docs/`, "my docs/a.txt", false, true, `@"my docs/a.txt" `, 17}, // the closing quote is reused
		{"@x tail", 2, "@x", "x.go", false, false, "@x.go  tail", 6},
		{"한글 @파", 5, "@파", "파일.md", false, false, "한글 @파일.md ", 10},
	} {
		got, cur := apply(c.text, c.cursor, c.prefix, c.path, c.dir, c.quoted)
		if got != c.want || cur != c.wantCur {
			t.Errorf("%q + %q = %q (cursor %d); want %q (%d)", c.text, c.path, got, cur, c.want, c.wantCur)
		}
	}
}
