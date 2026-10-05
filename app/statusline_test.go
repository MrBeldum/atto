package app

import (
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/ai"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/tui"
)

func statusApp(t *testing.T, cost *ai.ModelCost) *App {
	t.Helper()
	ref := config.ModelRef{Model: config.Model{ID: "m", Name: "Orca", ContextWindow: 262000, Cost: cost}}
	a := &App{ui: tui.New(nil), agent: agent.New(ref, "", t.TempDir()), cwd: "/work/proj", gitBranch: "main", sessName: "fix"}
	a.ctxTokens = 31000
	a.usage.add(provider.Usage{PromptTokens: 94000, CachedTokens: 80000, CacheWriteTokens: 2000, CompletionTokens: 3400, Cost: 0.1234})
	return a
}

func TestCompactTokens(t *testing.T) {
	for n, want := range map[int]string{0: "0", 950: "950", 1234: "1.2k", 2000: "2k", 12400: "12k", 12500: "13k", 1234567: "1.2M", 12345678: "12M"} {
		if got := compactTokens(n); got != want {
			t.Errorf("compactTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestBuiltinStatusWidths(t *testing.T) {
	price := &ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1, Output: 2}}
	a := statusApp(t, price)

	row := func(width int) string { return tui.StripEscapes(a.builtinStatus(width)) }
	wide := row(160)
	for _, want := range []string{"Orca", "11%", "31.0k/262.0k", "cache 85%", "↑12k", "↓3.4k", "R80k", "W2k", "$0.123", "/work/proj (main)", "fix"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide status lacks %q: %q", want, wide)
		}
	}

	// Narrower terminals lose the least important items first.
	order := []string{"fix", "R80k", "cache 85%", "↑12k", "$0.123", "/work/proj"}
	prev := len(order)
	for width := 160; width >= 20; width -= 4 {
		s := row(width)
		if w := tui.VisibleWidth(s); w > width {
			t.Fatalf("width %d: row is %d wide: %q", width, w, s)
		}
		if !strings.Contains(s, "Orca") {
			t.Fatalf("width %d: the model is always shown: %q", width, s)
		}
		// Count how many of the items in drop order are still there; it
		// must never grow as the terminal narrows.
		n := 0
		for _, it := range order {
			if strings.Contains(s, it) {
				n++
			}
		}
		if n > prev {
			t.Fatalf("width %d: an item came back: %q", width, s)
		}
		prev = n
	}
	if s := row(60); strings.Contains(s, "R80k") || strings.Contains(s, "fix") || !strings.Contains(s, "11%") {
		t.Errorf("width 60 keeps the bar and drops cache totals and the name: %q", s)
	}
	if s := row(40); strings.Contains(s, "proj") || !strings.Contains(s, "Orca") {
		t.Errorf("width 40: %q", s)
	}
}

func TestStatusCostOnlyWithPrices(t *testing.T) {
	a := statusApp(t, nil)
	a.usage.cost = 0
	if s := tui.StripEscapes(a.builtinStatus(160)); strings.Contains(s, "$") {
		t.Errorf("a model without prices shows no cost: %q", s)
	}
	a = statusApp(t, &ai.ModelCost{ModelCostRates: ai.ModelCostRates{Output: 5}})
	if s := tui.StripEscapes(a.builtinStatus(160)); !strings.Contains(s, "$0.123") {
		t.Errorf("a priced model shows the cost: %q", s)
	}
}

func TestStatusNoUsageYet(t *testing.T) {
	a := statusApp(t, nil)
	a.usage = usageStats{}
	s := tui.StripEscapes(a.builtinStatus(100))
	for _, no := range []string{"↑", "↓", "R0", "W0", "$"} {
		if strings.Contains(s, no) {
			t.Errorf("fresh session shows %q: %q", no, s)
		}
	}
}
