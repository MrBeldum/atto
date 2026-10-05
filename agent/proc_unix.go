//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// procTree kills a command's whole process group on cancel.
type procTree struct{}

func newProcTree(cmd *exec.Cmd) *procTree {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return &procTree{}
}

func (*procTree) started() {}
func (*procTree) close()   {}
