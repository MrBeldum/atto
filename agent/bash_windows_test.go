//go:build windows

package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunShellPowerShell(t *testing.T) {
	res := RunBash(context.Background(), t.TempDir(), nil, BashArgs{Command: "Write-Output 'out'; [Console]::Error.WriteLine('err'); Write-Output '한글'; exit 3"}, nil)
	if res.ExitCode != 3 {
		t.Fatalf("exit %d: %+v", res.ExitCode, res)
	}
	for _, want := range []string{"out", "err", "한글"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output %q lacks %q", res.Output, want)
		}
	}
}

func TestRunShellTimeoutKillsTree(t *testing.T) {
	start := time.Now()
	res := RunBash(context.Background(), t.TempDir(), nil, BashArgs{Command: "Start-Process -NoNewWindow powershell -ArgumentList '-Command','Start-Sleep 30'; Start-Sleep 30", Timeout: 2}, nil)
	if !res.TimedOut {
		t.Fatalf("expected timeout: %+v", res)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %s", d)
	}
}
