package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/sebastianrcnt/atto/config"
)

// cmdDebug saves a heap profile, the goroutines and the memory statistics
// of this atto to ~/.atto/debug/<time>/, for `go tool pprof` (or an agent)
// to read. Nothing leaves the machine.
func (a *App) cmdDebug(string) {
	dir, err := writeDebug(filepath.Join(config.Dir(), "debug", time.Now().Format("20060102-150405")))
	if err != nil {
		a.notice("debug: %v", err)
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	a.notice("Saved a heap profile to %s\n%s", shortPath(dir), memSummary(&m))
}

// writeDebug writes heap.pprof, goroutines.txt and memstats.txt to dir.
func writeDebug(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	runtime.GC() // the profile shows what is live, not garbage not yet collected
	write := func(name string, fn func(*os.File) error) error {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		defer f.Close()
		return fn(f)
	}
	if err := write("heap.pprof", func(f *os.File) error { return pprof.Lookup("heap").WriteTo(f, 0) }); err != nil {
		return "", err
	}
	if err := write("goroutines.txt", func(f *os.File) error { return pprof.Lookup("goroutine").WriteTo(f, 1) }); err != nil {
		return "", err
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	err := write("memstats.txt", func(f *os.File) error {
		_, err := fmt.Fprintf(f, "before GC: %s\nafter GC:  %s\n", memSummary(&before), memSummary(&after))
		return err
	})
	return dir, err
}

// memSummary is the memory figures that matter: what the program holds
// (heap in use), what the heap has from the OS, and everything the Go
// runtime has from the OS (close to the process's size).
func memSummary(m *runtime.MemStats) string {
	mb := func(b uint64) string { return fmt.Sprintf("%.1f MB", float64(b)/(1<<20)) }
	return fmt.Sprintf("heap in use %s · heap from the OS %s · total from the OS %s · %d goroutines · %d GCs",
		mb(m.HeapInuse), mb(m.HeapSys), mb(m.Sys), runtime.NumGoroutine(), m.NumGC)
}
