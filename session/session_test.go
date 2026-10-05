package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sebastianrcnt/atto/provider"
)

func TestWriterLoadList(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/work")
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatal("file should be created lazily")
	}
	w.Append(Entry{Type: TypeMessage, Message: &provider.Message{Role: "user", Content: "hello there"}})
	w.Append(Entry{Type: TypeMessage, Message: &provider.Message{Role: "assistant", Content: "hi"}, Usage: &provider.Usage{PromptTokens: 5}})
	w.Close()

	// A crash mid-write leaves a partial line; it must be ignored.
	f, _ := os.OpenFile(w.Path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"message","mess`)
	f.Close()

	h, entries, err := Load(w.Path)
	if err != nil || h.ID != w.ID || h.Cwd != "/work" || len(entries) != 2 {
		t.Fatalf("load: %+v %d %v", h, len(entries), err)
	}
	if entries[1].Usage.PromptTokens != 5 {
		t.Fatalf("usage lost: %+v", entries[1])
	}

	// Resume appends to the same file.
	r := Resume(w.Path, h)
	r.Append(Entry{Type: TypeMessage, Message: &provider.Message{Role: "user", Content: "again"}})
	r.Close()

	l, err := List("/work", false)
	if err != nil || len(l) != 1 {
		t.Fatalf("list: %v %v", l, err)
	}
	if l[0].Preview != "hello there" || l[0].Messages != 3 {
		t.Fatalf("summary %+v", l[0])
	}
	if other, _ := List("/elsewhere", false); len(other) != 0 {
		t.Fatal("cwd filter failed")
	}
	if filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(w.Path)))) != filepath.Join(os.Getenv("ATTO_DIR"), "sessions") {
		t.Fatalf("unexpected layout %s", w.Path)
	}
}

func TestArchiveAndName(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	w.Append(Entry{Type: TypeMessage, Message: &provider.Message{Role: "user", Content: "x"}})
	w.Append(Entry{Type: TypeName, Name: "first"})
	w.Append(Entry{Type: TypeName, Name: "renamed"})
	w.Close()
	if l, _ := List("", false); len(l) != 1 || l[0].Name != "renamed" {
		t.Fatalf("name: %+v", l)
	}
	p, err := Archive(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := List("", false); len(l) != 0 {
		t.Fatal("still active after archive")
	}
	if l, _ := List("", true); len(l) != 1 || !l[0].Archived || l[0].Path != p {
		t.Fatalf("archived list %+v", l)
	}
	back, err := Unarchive(p)
	if err != nil || back != w.Path {
		t.Fatalf("unarchive %s %v", back, err)
	}
}
