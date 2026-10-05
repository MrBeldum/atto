package ai

import (
	"fmt"
	"testing"
)

func TestRecentRequests(t *testing.T) {
	for i := 0; i < keepRecent+3; i++ {
		recordRequest("u", []byte(fmt.Sprint(i)))
	}
	r := RecentRequests()
	if len(r) != keepRecent || string(r[0].Body) != "3" || string(r[len(r)-1].Body) != fmt.Sprint(keepRecent+2) {
		t.Fatalf("ring: %d, first %s", len(r), r[0].Body)
	}
	PinRecentRequests("compaction", 2)
	for i := 0; i < keepRecent; i++ {
		recordRequest("u", []byte("later"))
	}
	p := PinnedRequests()["compaction"]
	if len(p) != 2 || string(p[1].Body) != fmt.Sprint(keepRecent+2) {
		t.Fatalf("pinned: %v", p)
	}
}
