package app

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// rssBytes is the process's resident set size, sampled in the background.
var rssBytes atomic.Int64

// sampleRSS reads the current resident set size. Linux reads /proc; other
// platforms ask ps (macOS has no cheap equivalent without cgo).
func sampleRSS() int64 {
	if runtime.GOOS == "linux" {
		if b, err := os.ReadFile("/proc/self/statm"); err == nil {
			if f := strings.Fields(string(b)); len(f) > 1 {
				if pages, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					return pages * int64(os.Getpagesize())
				}
			}
		}
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

// startMemoryMonitor samples RSS every interval until done is closed.
func startMemoryMonitor(interval time.Duration, done <-chan struct{}, onSample func()) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			rssBytes.Store(sampleRSS())
			onSample()
			select {
			case <-done:
				return
			case <-t.C:
			}
		}
	}()
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + "GB"
	case n >= 1<<20:
		return strconv.FormatInt(n>>20, 10) + "MB"
	case n > 0:
		return strconv.FormatInt(n>>10, 10) + "KB"
	}
	return "?"
}
