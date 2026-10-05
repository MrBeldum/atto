//go:build !windows

package app

func platformRSS() (int64, bool) { return 0, false }
