//go:build !windows

package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunBashTimeoutKillsGroup(t *testing.T) {
	start := time.Now()
	// The background child would keep the pipe open without the group kill.
	res := RunBash(context.Background(), t.TempDir(), nil, BashArgs{Command: "sleep 30 & sleep 30", Timeout: 1}, nil)
	if !res.TimedOut {
		t.Fatalf("expected timeout, got %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}
	if out := res.ForModel(BashArgs{Timeout: 1}); !strings.Contains(out, "timed out after 1s") {
		t.Fatalf("model output %q", out)
	}
}

func TestRunBashExitCodeAndStreaming(t *testing.T) {
	var chunks strings.Builder
	res := RunBash(context.Background(), t.TempDir(), nil, BashArgs{Command: "echo out; echo err >&2; exit 3"}, func(s string) { chunks.WriteString(s) })
	if res.ExitCode != 3 || res.Output != "out\nerr\n" || chunks.String() != res.Output {
		t.Fatalf("got %+v, streamed %q", res, chunks.String())
	}
	if out := res.ForModel(BashArgs{}); !strings.HasSuffix(out, "[exit code 3]") {
		t.Fatalf("model output %q", out)
	}
}

func TestRunBashCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	res := RunBash(ctx, t.TempDir(), nil, BashArgs{Command: "sleep 30"}, nil)
	if !res.Canceled {
		t.Fatalf("expected canceled, got %+v", res)
	}
}

func TestTruncateTail(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("line\n")
	}
	out := truncateTail(b.String())
	if !strings.HasPrefix(out, "[output truncated: showing last") || !strings.Contains(out, "full output: ") {
		t.Fatalf("header: %q", out[:120])
	}
	if n := strings.Count(out, "\n"); n > maxOutputLines+1 {
		t.Fatalf("%d lines kept", n)
	}
}
