package session

import "testing"

func TestBranchSummaryEntry(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	w := New("/w")
	for _, e := range []Entry{msg("user", "u1"), msg("assistant", "a1"), msg("user", "u2"), msg("assistant", "a2")} {
		w.Append(e)
	}
	_, entries, _ := Load(w.Path)
	a1, u2, a2 := find(t, entries, "a1").ID, find(t, entries, "u2").ID, find(t, entries, "a2").ID
	if got := contents(Abandoned(entries, a2, a1)); got != "u2 a2" {
		t.Fatalf("abandoned %q", got)
	}
	if got := Abandoned(entries, a1, a2); len(got) != 0 {
		t.Fatalf("moving forward leaves nothing: %v", got)
	}
	w.BranchSummary(a1, Entry{Summary: "tried u2"})
	w.Append(msg("user", "u3"))
	w.Close()
	_, entries, _ = Load(w.Path)
	s := entries[4]
	if s.Type != TypeBranchSummary || s.Parent != a1 || s.FromID != a2 || s.Summary != "tried u2" {
		t.Fatalf("summary entry %+v", s)
	}
	if got := contents(Active(entries)); got != "u1 a1 [branch_summary] u3" {
		t.Fatalf("active %q", got)
	}
	if got := contents(Abandoned(entries, Leaf(entries), u2)); got != "[branch_summary] u3" {
		t.Fatalf("abandoned %q", got)
	}
	var found bool
	for _, it := range Items(entries) {
		found = found || (it.Label == "branch summary" && it.Text == "tried u2")
	}
	if !found {
		t.Fatal("history should list the summary")
	}
}
