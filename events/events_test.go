package events

import (
	"strings"
	"testing"
	"time"
)

func TestInboxAndTimers(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	Push("s", Event{Source: "job", Text: "first"})
	Push("s", Event{Source: "job", Text: "second"})
	if !Pending("s") {
		t.Fatal("pending")
	}
	evs := Drain("s")
	if len(evs) != 2 || evs[0].Text != "first" || Pending("s") {
		t.Fatalf("drain %+v", evs)
	}
	if Format(evs) != Prefix+"first\n\n"+Prefix+"second" {
		t.Fatalf("format %q", Format(evs))
	}

	now := time.Now()
	soon, _ := AddTimer("s", now.Add(time.Second), "check CI")
	AddTimer("s", now.Add(time.Hour), "later")
	if n := FireDue("s", now); n != 0 {
		t.Fatalf("fired early: %d", n)
	}
	if n := FireDue("s", now.Add(2*time.Second)); n != 1 {
		t.Fatalf("fired %d", n)
	}
	evs = Drain("s")
	if len(evs) != 1 || !strings.Contains(evs[0].Text, "check CI") || len(Timers("s")) != 1 {
		t.Fatalf("timer event %+v", evs)
	}
	if CancelTimer("s", soon.ID) == nil {
		t.Fatal("fired timer should be gone")
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.Local)
	if d, _ := ParseWhen("10m", now); !d.Equal(now.Add(10 * time.Minute)) {
		t.Fatal(d)
	}
	if d, _ := ParseWhen("15:30", now); d.Hour() != 15 || d.Day() != 5 {
		t.Fatal(d)
	}
	if d, _ := ParseWhen("09:00", now); d.Day() != 6 {
		t.Fatal("past clock time should mean tomorrow", d)
	}
	if _, err := ParseWhen("soon", now); err == nil {
		t.Fatal("want error")
	}
}
