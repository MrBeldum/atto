package session

import (
	"os"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/provider"
)

func msg(role, content string) Entry {
	return Entry{Type: TypeMessage, Message: &provider.Message{Role: role, Content: content}}
}

func contents(entries []Entry) string {
	var out []string
	for _, e := range entries {
		switch {
		case e.Message != nil:
			out = append(out, e.Message.Content)
		default:
			out = append(out, "["+e.Type+"]")
		}
	}
	return strings.Join(out, " ")
}

func find(t *testing.T, entries []Entry, content string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Message != nil && e.Message.Content == content {
			return e
		}
	}
	t.Fatalf("no entry %q", content)
	return Entry{}
}

func TestLegacyFileLoadsAsOneBranch(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	w.Close()
	// A file from before entries had IDs.
	os.MkdirAll(dirOf(w.Path), 0o755)
	os.WriteFile(w.Path, []byte(`{"type":"session","id":"abc","cwd":"/w","version":1}
{"type":"message","message":{"role":"user","content":"u1"}}
{"type":"message","message":{"role":"assistant","content":"a1"}}
`), 0o644)
	h, entries, err := Load(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].ID != "#1" || entries[0].Parent != "" || entries[1].ID != "#2" || entries[1].Parent != "#1" {
		t.Fatalf("not linked: %+v", entries)
	}
	r := Resume(w.Path, h)
	r.SetLeaf(Leaf(entries))
	r.Append(msg("user", "u2"))
	r.Close()
	_, entries, _ = Load(w.Path)
	if got := contents(Active(entries)); got != "u1 a1 u2" {
		t.Fatalf("active %q", got)
	}
	if entries[2].Parent != "#2" || len(entries[2].ID) != 16 {
		t.Fatalf("appended entry %+v", entries[2])
	}
	raw, _ := os.ReadFile(w.Path)
	if !strings.HasPrefix(string(raw), `{"type":"session","id":"abc","cwd":"/w","version":1}
{"type":"message","message":{"role":"user","content":"u1"}}
`) {
		t.Fatal("old lines were rewritten")
	}
}

func dirOf(p string) string { return p[:strings.LastIndexAny(p, `/\`)] }

func TestBranchKeepsBothBranches(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	for _, e := range []Entry{msg("user", "u1"), msg("assistant", "a1"), msg("user", "u2"), msg("assistant", "a2")} {
		w.Append(e)
	}
	_, entries, _ := Load(w.Path)
	size0, _ := os.Stat(w.Path)

	// Pick u2 in the tree: back to its parent, text for the editor.
	leaf, text, ok := BranchPoint(entries, find(t, entries, "u2").ID)
	if !ok || text != "u2" || leaf != find(t, entries, "a1").ID {
		t.Fatalf("branch point %q %q %v", leaf, text, ok)
	}
	w.Branch(leaf)
	w.Append(msg("user", "u2 edited"))
	w.Close()

	_, entries, _ = Load(w.Path)
	if got := contents(Active(entries)); got != "u1 a1 [branch] u2 edited" {
		t.Fatalf("active %q", got)
	}
	if st, _ := os.Stat(w.Path); st.Size() <= size0.Size() {
		t.Fatal("file should only grow")
	}
	on := OnActivePath(entries)
	if on[2] || on[3] || !on[1] || !on[5] {
		t.Fatalf("on path %v", on)
	}
	for _, it := range Items(entries) {
		if (it.Text == "u2" || it.Text == "a2") != it.OffBranch {
			t.Errorf("item %+v", it)
		}
	}
	roots := Tree(entries)
	if len(roots) != 1 || len(roots[0].Children) != 1 {
		t.Fatalf("roots %+v", roots)
	}
	if a1 := roots[0].Children[0]; len(a1.Children) != 2 || a1.Children[0].Entry.Message.Content != "u2" || a1.Children[1].Entry.Type != TypeBranch {
		t.Fatalf("a1 children %+v", a1.Children)
	}
	// The list shows the branch the session continues on.
	if l, _ := List("/w", false); len(l) != 1 || l[0].Messages != 3 {
		t.Fatalf("summary %+v", l)
	}

	// Picking the first message goes back to before everything.
	leaf, _, _ = BranchPoint(entries, find(t, entries, "u1").ID)
	r := Resume(w.Path, Entry{ID: w.ID})
	r.SetLeaf(Leaf(entries))
	r.Branch(leaf)
	r.Close()
	_, entries, _ = Load(w.Path)
	if got := contents(Active(entries)); got != "[branch]" {
		t.Fatalf("root branch: %q", got)
	}
	if len(Tree(entries)) != 2 {
		t.Fatal("going back to the start adds a root")
	}
	if l, _ := List("/w", false); len(l) != 1 || l[0].Messages != 0 || l[0].Preview != "u1" {
		t.Fatalf("a session back at its start should stay listed: %+v", l)
	}
}

func TestBranchBeforeCompaction(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	w.Append(msg("user", "u1"))
	w.Append(msg("assistant", "a1"))
	w.Append(Entry{Type: TypeCompaction, Notes: "n", Replacement: []provider.Message{{Role: "user", Content: "notes"}}})
	w.Append(msg("user", "u2"))
	_, entries, _ := Load(w.Path)
	w.Branch(find(t, entries, "a1").ID) // pick a1: it becomes the leaf
	w.Close()
	_, entries, _ = Load(w.Path)
	if got := contents(Active(entries)); got != "u1 a1 [branch]" {
		t.Fatalf("active %q", got)
	}
}

func TestLabelsAndFork(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	w.Append(msg("user", "u1"))
	w.Append(msg("assistant", "a1"))
	_, entries, _ := Load(w.Path)
	a1 := find(t, entries, "a1").ID
	w.Append(Entry{Type: TypeLabel, TargetID: a1, Label: "good"})
	w.Append(msg("user", "u2"))
	w.Close()
	_, entries, _ = Load(w.Path)
	if n := Tree(entries)[0].Children[0]; n.Label != "good" || n.LabelTime.IsZero() {
		t.Fatalf("label %+v", n)
	}

	leaf, _, _ := BranchPoint(entries, find(t, entries, "u2").ID)
	f := Fork(w.Path, "/w", entries, leaf)
	f.Close()
	h, forked, err := Load(f.Path)
	if err != nil || h.ParentSession != w.Path {
		t.Fatalf("fork header %+v %v", h, err)
	}
	if got := contents(Active(forked)); got != "u1 a1 [label]" {
		t.Fatalf("forked %q", got)
	}
	if forked[2].TargetID != forked[1].ID {
		t.Fatal("label should point at the copied entry")
	}
}
