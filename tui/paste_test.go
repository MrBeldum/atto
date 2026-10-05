package tui

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func feedKeys(e *Editor, keys ...string) {
	for _, k := range keys {
		e.HandleInput(k)
	}
}

func submitter(e *Editor) (*string, *[]Attachment) {
	var text string
	var att []Attachment
	e.OnSubmit = func(s string, a []Attachment) { text, att = s, a }
	return &text, &att
}

func TestBracketedPasteParsing(t *testing.T) {
	var p inputParser
	big := strings.Repeat("x", 5000) + "\n" + strings.Repeat("y", 5000)
	// Split across reads, including inside the end marker.
	got := p.feed("\x1b[200~" + big[:3000])
	got = append(got, p.feed(big[3000:]+"\x1b[20")...)
	got = append(got, p.feed("1~\r")...)
	if !slices.Equal(got, []string{PastePrefix + big, "\r"}) {
		t.Fatalf("large paste: got %d events", len(got))
	}
	got = p.feed("\x1b[200~a\r\nb\x1b[201~")
	if !slices.Equal(got, []string{PastePrefix + "a\r\nb"}) {
		t.Fatalf("small paste: %q", got)
	}
}

func TestUnbracketedPasteBurst(t *testing.T) {
	now := time.Unix(0, 0)
	p := inputParser{now: func() time.Time { return now }}
	// Typed keys, and a line typed then submitted, are not pastes.
	for _, in := range []string{"a", "\r", "hi\r", "\x1b[A", "x\x7f"} {
		if got := p.feed(in); strings.HasPrefix(got[0], PastePrefix) {
			t.Fatalf("%q read as a paste", in)
		}
	}
	// A console without bracketed paste delivers lines in one read.
	if got := p.feed("line1\rline2"); !slices.Equal(got, []string{PastePrefix + "line1\rline2"}) {
		t.Fatalf("burst: %q", got)
	}
	// Its tail, arriving right after, belongs to it, even a lone newline.
	now = now.Add(5 * time.Millisecond)
	if got := p.feed("\r"); !slices.Equal(got, []string{PastePrefix + "\r"}) {
		t.Fatalf("burst tail: %q", got)
	}
	now = now.Add(time.Second)
	if got := p.feed("\r"); !slices.Equal(got, []string{"\r"}) {
		t.Fatalf("later enter: %q", got)
	}
}

func TestSmallPasteInsertsWithoutSubmitting(t *testing.T) {
	e := NewEditor("> ")
	text, _ := submitter(e)
	feedKeys(e, "a", PastePrefix+"one\r\ntwo\rthree\n", "b")
	if e.Text() != "aone\ntwo\nthree\nb" || *text != "" {
		t.Fatalf("text %q submitted %q", e.Text(), *text)
	}
}

func TestLargePastePlaceholder(t *testing.T) {
	e := NewEditor("> ")
	text, _ := submitter(e)
	big := strings.Repeat("é", LargePaste+1)
	feedKeys(e, "<", PastePrefix+big, PastePrefix+big, ">")
	want := "<[Pasted Content 1001 chars][Pasted Content 1001 chars #2]>"
	if e.Text() != want {
		t.Fatalf("buffer %q", e.Text())
	}
	// Exactly the threshold inserts normally.
	feedKeys(e, PastePrefix+strings.Repeat("z", LargePaste))
	if !strings.HasSuffix(e.Text(), ">"+strings.Repeat("z", LargePaste)) {
		t.Fatal("paste at the threshold should be inline")
	}
	e.SetText(want)
	// Backspace after a placeholder deletes all of it; the label of the
	// next paste of that size is reused.
	feedKeys(e, "\x1b[D", "\x7f")
	if e.Text() != "<[Pasted Content 1001 chars]>" {
		t.Fatalf("after delete %q", e.Text())
	}
	feedKeys(e, PastePrefix+big)
	if e.Text() != "<[Pasted Content 1001 chars][Pasted Content 1001 chars #2]>" {
		t.Fatalf("relabel %q", e.Text())
	}
	feedKeys(e, "\r")
	if *text != "<"+big+big+">" {
		t.Fatalf("expanded to %d chars", len([]rune(*text)))
	}
	// History recall brings the placeholders back with their content.
	feedKeys(e, "\x1b[A", "\r")
	if *text != "<"+big+big+">" {
		t.Fatal("history lost the paste")
	}
}

func TestPlaceholderIsAtomic(t *testing.T) {
	e := NewEditor("> ")
	label := e.AttachImage("1024x768 PNG", "img")
	if label != "[image 1: 1024x768 PNG]" || e.Text() != label+" " {
		t.Fatalf("label %q text %q", label, e.Text())
	}
	feedKeys(e, "x")
	// Cursor movement skips over the placeholder.
	feedKeys(e, "\x1b[H", "\x1b[C")
	if e.pos != len([]rune(label)) {
		t.Fatalf("right moved to %d", e.pos)
	}
	feedKeys(e, "\x1b[D")
	if e.pos != 0 {
		t.Fatalf("left moved to %d", e.pos)
	}
	// Typing never lands inside; delete at its start removes it whole.
	feedKeys(e, "\x1b[3~")
	if e.Text() != " x" || len(e.Attachments()) != 0 {
		t.Fatalf("after delete %q %v", e.Text(), e.Attachments())
	}

	// Word deletion that cuts into a placeholder takes all of it.
	e.SetText("")
	e.AttachImage("1x1 PNG", "a")
	feedKeys(e, "y", "o")
	feedKeys(e, "\x1b[D", "\x1b[D", "\x1b[D") // into "[image 1: 1x1 PNG]| yo"
	feedKeys(e, "\x17")                       // ctrl+w
	if strings.Contains(e.Text(), "image") || len(e.Attachments()) != 0 {
		t.Fatalf("ctrl+w left %q", e.Text())
	}
}

func TestImageAttachmentsSubmit(t *testing.T) {
	e := NewEditor("> ")
	text, att := submitter(e)
	feedKeys(e, "a")
	e.AttachImage("10x10 PNG", "first")
	e.AttachImage("20x20 JPEG", "second")
	if e.Text() != "a[image 1: 10x10 PNG] [image 2: 20x20 JPEG] " {
		t.Fatalf("buffer %q", e.Text())
	}
	// Deleting image 1 removes it; the next image is numbered after the
	// highest remaining one.
	e.SetText(strings.Replace(e.Text(), "[image 1: 10x10 PNG] ", "", 1))
	if l := e.AttachImage("5x5 PNG", "third"); l != "[image 3: 5x5 PNG]" {
		t.Fatalf("label %q", l)
	}
	feedKeys(e, "\r")
	if *text != "a[image 2: 20x20 JPEG] [image 3: 5x5 PNG]" || len(*att) != 2 ||
		(*att)[0].Value != "second" || (*att)[1].Value != "third" {
		t.Fatalf("submitted %q %v", *text, *att)
	}
	// An image alone is a valid prompt.
	e.AttachImage("1x1 PNG", "only")
	feedKeys(e, "\r")
	if len(*att) != 1 || *text != "[image 1: 1x1 PNG]" {
		t.Fatalf("image-only %q %v", *text, *att)
	}
}

func TestPasteHookTakesImagePaths(t *testing.T) {
	e := NewEditor("> ")
	e.OnPaste = func(p string) bool {
		if strings.HasSuffix(p, ".png") {
			e.AttachImage("3x3 PNG", p)
			return true
		}
		return false
	}
	feedKeys(e, PastePrefix+"/tmp/shot.png", PastePrefix+"plain")
	if e.Text() != "[image 1: 3x3 PNG] plain" || e.Attachments()[0].Value != "/tmp/shot.png" {
		t.Fatalf("buffer %q", e.Text())
	}
}

func TestEditorRendersPlaceholderInColor(t *testing.T) {
	e := NewEditor("> ")
	e.AttachImage("1x1 PNG", nil)
	lines := e.Render(80)
	if !strings.Contains(lines[1], placeholderOn+"[image 1: 1x1 PNG]"+placeholderOff) {
		t.Fatalf("render %q", lines[1])
	}
	// Narrow: the label wraps and every row closes its color.
	for _, l := range e.Render(10)[1:] {
		if strings.Count(l, placeholderOn) != strings.Count(l, placeholderOff) {
			t.Fatalf("unbalanced row %q", l)
		}
	}
}
