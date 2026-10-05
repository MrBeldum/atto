package session

import "testing"

// A fork keeps the display data of its messages, pointing at their new IDs.
func TestForkCarriesBlockDisplay(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	w.Append(msg("user", "u1"))
	w.Append(msg("assistant", "a1"))
	_, entries, _ := Load(w.Path)
	a1 := find(t, entries, "a1").ID
	w.Append(Entry{Type: TypeBlockDisplay, TargetID: a1, Block: BlockText, Ext: "x", Display: "A1"})
	w.Close()
	_, entries, _ = Load(w.Path)
	f := Fork(w.Path, "/w", entries, Leaf(entries))
	f.Close()
	_, forked, _ := Load(f.Path)
	if got := contents(Active(forked)); got != "u1 a1 [block_display]" {
		t.Fatalf("forked %q", got)
	}
	if d := forked[2]; d.TargetID != forked[1].ID || d.Display != "A1" || d.Block != BlockText || d.Ext != "x" {
		t.Fatalf("display %+v, message %s", d, forked[1].ID)
	}
}
