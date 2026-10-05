package ai

import (
	"bytes"
	"compress/gzip"
	"io"
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

// keepRecent is how many of the latest requests /debug can save, and
// maxRecentBytes how many compressed bytes the ring may hold (the newest
// request always stays).
const (
	keepRecent     = 6
	maxRecentBytes = 4 << 20
)

// storedRequest is a SentRequest with its body gzipped: a long
// conversation's requests are about 1 MB each and nearly the same, so they
// compress well.
type storedRequest struct {
	Time time.Time
	URL  string
	gz   []byte
}

// recent keeps the latest requests in memory, compressed, plus sets pinned
// under a label so later requests don't push them out, such as the
// requests around the last compaction. Nothing is written to disk until
// /debug asks.
var recent struct {
	sync.Mutex
	ring   []storedRequest
	pinned map[string][]storedRequest
}

func compressBody(body []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(len(body) / 8)
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	zw.Write(body)
	zw.Close()
	return buf.Bytes()
}

func (r storedRequest) open() SentRequest {
	out := SentRequest{Time: r.Time, URL: r.URL}
	if zr, err := gzip.NewReader(bytes.NewReader(r.gz)); err == nil {
		out.Body, _ = io.ReadAll(zr)
	}
	return out
}

func recordRequest(url string, body []byte) {
	r := storedRequest{Time: time.Now(), URL: url, gz: compressBody(body)}
	recent.Lock()
	defer recent.Unlock()
	recent.ring = append(recent.ring, r)
	n, total := len(recent.ring), 0
	for i := n - 1; i >= 0; i-- {
		if total += len(recent.ring[i].gz); n-i > keepRecent || (total > maxRecentBytes && i < n-1) {
			recent.ring = append(recent.ring[:0:0], recent.ring[i+1:]...)
			return
		}
	}
}

func openAll(rs []storedRequest) []SentRequest {
	out := make([]SentRequest, len(rs))
	for i, r := range rs {
		out[i] = r.open()
	}
	return out
}

// RecentRequests returns the latest requests, oldest first.
func RecentRequests() []SentRequest {
	recent.Lock()
	rs := append([]storedRequest(nil), recent.ring...)
	recent.Unlock()
	return openAll(rs)
}

// PinRecentRequests keeps the latest n requests under label, replacing
// what was pinned there.
func PinRecentRequests(label string, n int) {
	recent.Lock()
	defer recent.Unlock()
	if recent.pinned == nil {
		recent.pinned = map[string][]storedRequest{}
	}
	r := recent.ring
	recent.pinned[label] = append([]storedRequest(nil), r[max(0, len(r)-n):]...)
}

// PinnedRequests returns the pinned requests by label.
func PinnedRequests() map[string][]SentRequest {
	recent.Lock()
	pinned := make(map[string][]storedRequest, len(recent.pinned))
	for k, v := range recent.pinned {
		pinned[k] = append([]storedRequest(nil), v...)
	}
	recent.Unlock()
	out := make(map[string][]SentRequest, len(pinned))
	for k, v := range pinned {
		out[k] = openAll(v)
	}
	return out
}
