package transcript

import (
	"reflect"
	"testing"
)

// Each extension has its own status; the latest text wins and only its
// owner restores the original; a saved entry is an extension's whole state.
func TestBlockDisplay(t *testing.T) {
	var d BlockDisplay
	if !d.IsZero() || d.SetStatus("a", "") || d.SetDisplay("a", "") {
		t.Fatal("zero value")
	}
	d.SetStatus("a", "one")
	d.SetStatus("b", "two")
	kept := d // a copy keeps what it had
	if !d.SetStatus("a", "uno") || d.SetStatus("a", "uno") {
		t.Fatal("status change reported wrong")
	}
	if !reflect.DeepEqual(d.Statuses, []BlockStatus{{"a", "uno"}, {"b", "two"}}) || kept.Statuses[0].Text != "one" {
		t.Fatalf("statuses %v, copy %v", d.Statuses, kept.Statuses)
	}
	d.SetDisplay("a", "A")
	d.SetDisplay("b", "B")
	if d.SetDisplay("a", "") || d.Owner != "b" || d.Text != "B" {
		t.Fatalf("not the owner restored it: %+v", d)
	}
	if st, text := d.Snapshot("b"); st != "two" || text != "B" {
		t.Fatalf("snapshot %q %q", st, text)
	}
	if !d.Apply(Display{Ext: "b"}) || d.Text != "" || !reflect.DeepEqual(d.Statuses, []BlockStatus{{"a", "uno"}}) {
		t.Fatalf("apply cleared b: %+v", d)
	}
	if d.IsZero() || !d.SetStatus("a", "") || !d.IsZero() {
		t.Fatalf("not zero: %+v", d)
	}
}
