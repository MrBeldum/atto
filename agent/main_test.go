package agent

import (
	"os"
	"testing"
)

// New scans the home directory for skills; keep tests off the real one.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "atto-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
