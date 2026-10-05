//go:build windows

package jobs

// ignoreSIGPIPE: on Windows a write to a closed pipe is an error, never a
// signal.
func ignoreSIGPIPE() {}
