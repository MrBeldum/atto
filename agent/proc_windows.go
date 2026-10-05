//go:build windows

package agent

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procTree puts a command in a job object so cancel kills everything it
// spawned (Windows has no process groups to signal). The job is created
// with KILL_ON_JOB_CLOSE, so closing it also cleans up stragglers.
type procTree struct {
	cmd *exec.Cmd
	job windows.Handle
}

func newProcTree(cmd *exec.Cmd) *procTree {
	t := &procTree{cmd: cmd}
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

// started assigns the running process to the job. Children spawned in the
// instant before assignment may escape; the process itself never does.
func (t *procTree) started() {
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

func (t *procTree) close() {
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
