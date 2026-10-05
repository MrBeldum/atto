//go:build !windows

package hooks

import (
	"os/exec"
	"syscall"
)

// killTreeOnCancel runs cmd in its own process group and, when its context
// ends, kills the whole group so children of the hook do not outlive it.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
