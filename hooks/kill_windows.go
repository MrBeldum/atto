//go:build windows

package hooks

import (
	"os/exec"
	"strconv"
)

// killTreeOnCancel kills the hook's whole process tree when its context
// ends, so children of the hook do not outlive it.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
