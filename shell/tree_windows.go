//go:build windows

package shell

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tree puts a command in a job object so cancel kills everything it
// spawned (Windows has no process groups to signal). The job is created
// with KILL_ON_JOB_CLOSE, so closing it also cleans up stragglers.
type Tree struct {
	cmd *exec.Cmd
	job windows.Handle
}

func NewTree(cmd *exec.Cmd) *Tree {
	t := &Tree{cmd: cmd}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err == nil {
			t.job = job
		} else {
			windows.CloseHandle(job)
		}
	}
	cmd.Cancel = func() error {
		if t.job != 0 {
			return windows.TerminateJobObject(t.job, 1)
		}
		return cmd.Process.Kill()
	}
	return t
}

// Started assigns the running process to the job. Children spawned in the
// instant before assignment may escape; the process itself never does.
func (t *Tree) Started() {
	if t.job == 0 || t.cmd.Process == nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.AssignProcessToJobObject(t.job, h)
}

func (t *Tree) Close() {
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}

// Kill terminates the whole tree now.
func (t *Tree) Kill() {
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	} else if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

// Detach makes cmd outlive its parent console.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

// Terminate stops a job supervisor. Windows has no SIGTERM, so the
// supervisor is terminated outright; its kill-on-close job object takes
// the command's whole tree down with it.
func Terminate(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// Alive reports whether pid is running.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}
