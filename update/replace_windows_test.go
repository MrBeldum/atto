//go:build windows

package update

import (
	"os"
	"path/filepath"
	"testing"
)

// An atto started before the last update still runs from atto.exe.old and
// keeps it from being replaced: the update moves atto.exe aside under
// another name instead of failing.
func TestReplaceWithOldInUse(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "atto.exe")
	_ = os.WriteFile(exe, []byte("current"), 0o755)
	_ = os.WriteFile(exe+".old", []byte("older"), 0o755)
	f, err := os.Open(exe + ".old") // open without delete sharing, like a running exe
	if err != nil {
		t.Fatal(err)
	}
	if err := replace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe = %q", b)
	}
	olds, _ := filepath.Glob(exe + ".old-*")
	if len(olds) != 1 {
		t.Fatalf("moved aside as %v", olds)
	}
	f.Close()
}
