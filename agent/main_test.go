package agent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sebastianrcnt/atto/jobs"
)

// The test binary doubles as atto for the processes commands run under:
// shell hosts (`_shell`) and job supervisors (`_supervise <dir>`), which
// are started by re-executing os.Executable(). With them, commands run
// as in the atto binary, under shell hosts.
//
// New scans the home directory for skills; tests keep off the real one.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "_shell":
			if err := jobs.ServeHost(os.Stdin, os.Stdout, os.Stderr); err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		case "_fakeatto": // TestBackgroundedCommandOutlivesAtto
			ShellHost = true
			res := RunBash(context.Background(), os.Getenv("FAKEATTO_DIR"), []string{"ATTO_SESSION_ID=" + os.Getenv("FAKEATTO_SESSION")},
				BashArgs{Description: "outlive", Command: os.Args[2], Timeout: 1}, nil)
			fmt.Println(res.ForModel(BashArgs{Timeout: 1}))
			os.Exit(0) // without waiting for the job
		case "_supervise":
			if len(os.Args) != 3 || jobs.Supervise(os.Args[2]) != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	ShellHost = true
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
