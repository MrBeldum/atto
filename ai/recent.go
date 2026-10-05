package ai

import (
	"sync"
	"time"
)

// SentRequest is a model request as sent: the URL and the JSON body. No
// headers are kept, so no API keys.
type SentRequest struct {
	Time time.Time
	URL  string
	Body []byte
}

// keepRecent is how many of the latest requests /debug can save.
const keepRecent = 6

// recent keeps the latest requests in memory (bodies are shared, not
// copied: nothing writes to them after they are sent), plus sets pinned
// under a label so later requests don't push them out, such as the
// requests around the last compaction. Nothing is written to disk until
// /debug asks.
var recent struct {
	sync.Mutex
	ring   []SentRequest
	pinned map[string][]SentRequest
}

func recordRequest(url string, body []byte) {
	recent.Lock()
	defer recent.Unlock()
	recent.ring = append(recent.ring, SentRequest{Time: time.Now(), URL: url, Body: body})
	if n := len(recent.ring); n > keepRecent {
		recent.ring = append(recent.ring[:0:0], recent.ring[n-keepRecent:]...)
	}
}

// RecentRequests returns the latest requests, oldest first.
func RecentRequests() []SentRequest {
	recent.Lock()
	defer recent.Unlock()
	return append([]SentRequest(nil), recent.ring...)
}

// PinRecentRequests keeps the latest n requests under label, replacing
// what was pinned there.
func PinRecentRequests(label string, n int) {
	recent.Lock()
	defer recent.Unlock()
	if recent.pinned == nil {
		recent.pinned = map[string][]SentRequest{}
	}
	r := recent.ring
	recent.pinned[label] = append([]SentRequest(nil), r[max(0, len(r)-n):]...)
}

// PinnedRequests returns the pinned requests by label.
func PinnedRequests() map[string][]SentRequest {
	recent.Lock()
	defer recent.Unlock()
	out := make(map[string][]SentRequest, len(recent.pinned))
	for k, v := range recent.pinned {
		out[k] = append([]SentRequest(nil), v...)
	}
	return out
}
