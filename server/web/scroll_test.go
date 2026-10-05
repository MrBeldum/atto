package web

import (
	"testing"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// The transcript's follow logic (src/scroll.ts), run in goja.
func TestScrollFollow(t *testing.T) {
	out := api.Build(api.BuildOptions{
		EntryPoints: []string{"src/scroll.ts"},
		Bundle:      true,
		Format:      api.FormatIIFE,
		GlobalName:  "scroll",
		Target:      api.ES2017,
	})
	if len(out.Errors) > 0 || len(out.OutputFiles) == 0 {
		t.Fatalf("build: %v", out.Errors)
	}
	vm := goja.New()
	if _, err := vm.RunString(string(out.OutputFiles[0].Contents)); err != nil {
		t.Fatal(err)
	}
	eval := func(expr string) any {
		t.Helper()
		v, err := vm.RunString(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		return v.Export()
	}
	// A transcript 2000px high in a 600px view: the bottom is at 1400.
	for expr, want := range map[string]any{
		"scroll.atBottom(1400, 2000, 600)": true,
		"scroll.atBottom(1397, 2000, 600)": true, // rounding
		"scroll.atBottom(1360, 2000, 600)": false,
		// Moving up a little stops following: no zone that snaps back.
		"scroll.nextFollow(true, 1400, 1380, 2000, 600)": false,
		"scroll.nextFollow(true, 1400, 1399, 2000, 600)": true,
		// Moving down without reaching the bottom keeps what was.
		"scroll.nextFollow(false, 800, 900, 2000, 600)": false,
		"scroll.nextFollow(true, 800, 900, 2000, 600)":  true,
		// Back at the bottom follows again.
		"scroll.nextFollow(false, 900, 1400, 2000, 600)": true,
		// Output arrived (taller transcript), the reader did not move.
		"scroll.nextFollow(false, 900, 900, 2600, 600)": false,
		// The anchor is the first item still in view.
		"scroll.anchorIndex([0, 100, 300], [100, 200, 50], 150)": int64(1),
		"scroll.anchorIndex([0, 100, 300], [100, 200, 50], 0)":   int64(0),
		"scroll.anchorIndex([], [], 0)":                          int64(-1),
		// Something above grew by 120px: scroll down as much.
		"scroll.anchorShift(300, 420)":   int64(120),
		"scroll.anchorShift(300, 300.2)": int64(0),
	} {
		if got := eval(expr); got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
}
