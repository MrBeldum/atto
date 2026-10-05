package app

import (
	"strings"
	"testing"
	"time"

	"github.com/sebastianrcnt/atto/tui"
)

// The activity spinner moves one frame per animation tick, whatever the
// tick: full repaint ticks slower, and skipping frames looked jerky.
func TestSpinnerStepsOncePerTick(t *testing.T) {
	a := testApp(t)
	for _, full := range []bool{false, true} {
		a.ui.Do(func() {
			a.ui.FullRepaint = full
			a.busy, a.activity = true, "Thinking"
			step := a.ui.AnimationInterval()
			a.runStart = time.Now().Add(-3*step - step/2)
			got := tui.StripEscapes(a.renderActivity(80)[1])
			if !strings.HasPrefix(got, spinnerFrames[3]+" Thinking") {
				t.Errorf("full repaint %v: %q", full, got)
			}
			a.busy = false
		})
	}
}
