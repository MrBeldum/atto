package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Timer delivers Message to the session inbox at Due. Timers are files so
// the model can set them from a shell (`atto timer in 10m "..."`); the
// front end checks them while it runs, and overdue timers fire on resume.
type Timer struct {
	ID      string    `json:"id"`
	Due     time.Time `json:"due"`
	Message string    `json:"message"`
	Created time.Time `json:"created"`
}

func timerDir(session string) string { return filepath.Join(Dir(session), "timers") }

// AddTimer schedules a timer and returns it.
func AddTimer(session string, due time.Time, message string) (Timer, error) {
	if session == "" {
		return Timer{}, fmt.Errorf("no session")
	}
	t := Timer{ID: randID()[:6], Due: due, Message: message, Created: time.Now()}
	data, _ := json.Marshal(t)
	return t, writeAtomic(filepath.Join(timerDir(session), t.ID+".json"), data)
}

// Timers lists pending timers, soonest first.
func Timers(session string) []Timer {
	ents, _ := os.ReadDir(timerDir(session))
	var out []Timer
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(timerDir(session), e.Name()))
		var t Timer
		if err == nil && json.Unmarshal(data, &t) == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Due.Before(out[j].Due) })
	return out
}

// CancelTimer removes a timer by ID.
func CancelTimer(session, id string) error {
	err := os.Remove(filepath.Join(timerDir(session), id+".json"))
	if os.IsNotExist(err) {
		return fmt.Errorf("no timer %q", id)
	}
	return err
}

// FireDue moves due timers into the inbox and returns how many fired.
func FireDue(session string, now time.Time) int {
	n := 0
	for _, t := range Timers(session) {
		if t.Due.After(now) {
			continue
		}
		if CancelTimer(session, t.ID) != nil {
			continue // another consumer took it
		}
		text := fmt.Sprintf("Timer %s fired: %s (set %s ago)", t.ID, t.Message, now.Sub(t.Created).Round(time.Second))
		_ = Push(session, Event{Source: "timer", Text: text, Title: "⏱ " + t.Message})
		n++
	}
	return n
}

// ParseWhen turns "10m", "1h30m", "90s" or "15:04" (today, or tomorrow if
// past) into a due time.
func ParseWhen(s string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("duration must be positive")
		}
		return now.Add(d), nil
	}
	if t, err := time.ParseInLocation("15:04", s, now.Location()); err == nil {
		due := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !due.After(now) {
			due = due.Add(24 * time.Hour)
		}
		return due, nil
	}
	return time.Time{}, fmt.Errorf("bad time %q: use a duration like 10m or a clock time like 15:04", s)
}
