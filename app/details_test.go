package app

import (
	"strings"
	"testing"
)

func TestCtrlTLeavesNoTrace(t *testing.T) {
	a := testApp(t)
	before := strings.Join(a.ui.Body.Render(80), "\n")
	var body, status string
	a.ui.Do(func() { a.onInput("\x14") }) // ctrl+t
	a.ui.Do(func() {
		body = strings.Join(a.ui.Body.Render(80), "\n")
		status = strings.Join(a.renderStatus(80), "\n")
	})
	if !a.details.on || body != before {
		t.Errorf("ctrl+t should only toggle details: %q", body)
	}
	if strings.Contains(status, "details") {
		t.Errorf("status line advertises details: %q", status)
	}
}
