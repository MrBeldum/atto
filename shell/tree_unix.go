//go:build !windows

package shell

import (
	"os/exec"
	"syscall"
)

// Tree kills a command's whole process group on cancel.
type Tree struct{ cmd *exec.Cmd }

func NewTree(cmd *exec.Cmd) *Tree {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return &Tree{cmd}
}

func (*Tree) Started() {}
func (*Tree) Close()   {}

// Kill terminates the whole tree now.
func (t *Tree) Kill() {
	if t.cmd.Process != nil {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Detach makes cmd outlive its parent (new session, no controlling tty).
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// Terminate asks the process (a job supervisor) to stop; it kills its tree
// and records the result.
func Terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// Alive reports whether pid is running.
func Alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
