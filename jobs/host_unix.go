//go:build !windows

package jobs

import (
	"os/signal"
	"syscall"
)

func ignoreSIGPIPE() { signal.Ignore(syscall.SIGPIPE) }
