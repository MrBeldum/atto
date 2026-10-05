package extensions

import (
	"reflect"
	"testing"
)

// Status items and widgets keep the order they were first set in, and an
// extension's are cleared together.
func TestUIState(t *testing.T) {
	var u UIState
	if !u.Empty() || len(u.Status()) != 0 || len(u.Widgets()) != 0 {
		t.Fatal("zero value")
	}
	u.SetStatus("b/x", "1")
	u.SetStatus("a/x", "2")
	u.SetStatus("b/x", "3")
	u.SetWidget("a/w", []string{"l"})
	u.SetWidget("b/w", []string{})
	if want := []UIItem{{Key: "b/x", Text: "3"}, {Key: "a/x", Text: "2"}}; !reflect.DeepEqual(u.Status(), want) {
		t.Fatalf("status %v", u.Status())
	}
	u.Clear("b")
	if want := []UIItem{{Key: "a/x", Text: "2"}}; !reflect.DeepEqual(u.Status(), want) {
		t.Fatalf("after clear: %v", u.Status())
	}
	if want := []UIItem{{Key: "a/w", Lines: []string{"l"}}}; !reflect.DeepEqual(u.Widgets(), want) {
		t.Fatalf("widgets %v", u.Widgets())
	}
	u.SetStatus("a/x", "")
	u.SetWidget("a/w", nil)
	if !u.Empty() {
		t.Fatal("not empty")
	}
}
