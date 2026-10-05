package app

import (
	"strings"
	"testing"
)

func TestCtrlTNoticeNotStatusFlag(t *testing.T) {
	a := testApp(t)
	var body, status string
	a.ui.Do(func() { a.onInput("\x14") }) // ctrl+t
	a.ui.Do(func() {
		body = strings.Join(a.ui.Body.Render(80), "\n")
		status = strings.Join(a.renderStatus(80), "\n")
	})
	if !strings.Contains(body, "Details on") {
		t.Errorf("no notice: %q", body)
	}
	if strings.Contains(status, "details on") {
		t.Errorf("status line still advertises details: %q", status)
	}
	a.ui.Do(func() { a.onInput("\x14") })
	a.ui.Do(func() { body = strings.Join(a.ui.Body.Render(80), "\n") })
	if !strings.Contains(body, "Details off") {
		t.Errorf("no off notice: %q", body)
	}
}
