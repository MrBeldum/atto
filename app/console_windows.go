package app

import "golang.org/x/sys/windows"

// windowsBuild is the Windows build number, e.g. 26100.
func windowsBuild() int { return int(windows.RtlGetVersion().BuildNumber) }
