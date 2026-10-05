package cli

import "testing"

func TestUpdateWording(t *testing.T) {
	if got := latestLine("v0.0.2"); got != "atto v0.0.2 (latest)" {
		t.Error(got)
	}
	if got := availableLine("v0.0.1", "v0.0.2"); got != "atto v0.0.1 · v0.0.2 is available (atto update)" {
		t.Error(got)
	}
	if got := updatedLine("v0.0.1", "v0.0.2"); got != "Updated atto v0.0.1 → v0.0.2" {
		t.Error(got)
	}
	if got := availableLine("dev", "v0.0.2"); got != "atto dev · v0.0.2 is available (atto update)" {
		t.Error(got)
	}
}
