package web

import (
	"testing"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// jsModule bundles one of src/'s pure modules for goja, as global name.
func jsModule(t *testing.T, entry, name string) *goja.Runtime {
	t.Helper()
	out := api.Build(api.BuildOptions{
		EntryPoints: []string{entry},
		Bundle:      true,
		Format:      api.FormatIIFE,
		GlobalName:  name,
		Target:      api.ES2017,
	})
	if len(out.Errors) > 0 || len(out.OutputFiles) == 0 {
		t.Fatalf("build: %v", out.Errors)
	}
	vm := goja.New()
	if _, err := vm.RunString(string(out.OutputFiles[0].Contents)); err != nil {
		t.Fatal(err)
	}
	return vm
}

// The transcript store (src/transcript.ts): order, in-place updates, the
// blocks the view re-renders, and output kept bounded.
func TestTranscript(t *testing.T) {
	vm := jsModule(t, "src/transcript.ts", "tr")
	run := func(src string) any {
		t.Helper()
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v.Export()
	}
	run(`
var s = new tr.Transcript();
var items = [];
for (var i = 0; i < 120; i++) items.push({id: "i" + i, type: "agentMessage", text: "t" + i, status: "completed"});
s.reset(items);
var b1 = s.blocks().slice();
s.delta("i119", "!");
var b2 = s.blocks().slice();
s.upsert({id: "new", type: "userMessage", text: "hi"});
s.upsert({id: "i5", type: "agentMessage", text: "changed"});
var b3 = s.blocks().slice();
`)
	for expr, want := range map[string]any{
		"tr.CHUNK":                           int64(50),
		"b1.length":                          int64(3),
		"b1[2].length":                       int64(20),
		"b2[0] === b1[0] && b2[1] === b1[1]": true, // untouched blocks keep their arrays
		"b2[2] !== b1[2]":                    true,
		"b2[2][19].text":                     "t119!",
		"s.length":                           int64(121),
		"s.last().id":                        "new",
		"b3[0] !== b2[0] && b3[1] === b2[1]": true,
		"b3[0][5].text":                      "changed",
		"s.list()[5].id":                     "i5", // updated in place, not moved
		// a delta for an item never started is skipped
		"(s.delta('nope', 'x'), s.get('nope'))": nil,
	} {
		if got := run(expr); got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}

	// Running commands; output bounded like the server's.
	run(`
s.reset([]);
s.upsert({id: "c", type: "commandExecution", status: "inProgress", output: ""});
var r1 = s.running();
var chunk = new Array(1025).join("x"); // 1 KiB
for (var i = 0; i < 300; i++) s.delta("c", chunk);
var outLen = s.get("c").output.length;
s.upsert({id: "c", type: "commandExecution", status: "completed"});
var r2 = s.running();
s.upsert({id: "d", type: "commandExecution", status: "inProgress", pending: true});
var r3 = s.running();
`)
	for expr, want := range map[string]any{
		"r1":                           true,
		"r2":                           false,
		"r3":                           false, // still being written, not running
		"outLen <= 2 * tr.KEEP_OUTPUT": true,
		"outLen >= tr.KEEP_OUTPUT":     true,
		"s.blocks().length":            int64(1),
	} {
		if got := run(expr); got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
	// Last: reset changes the state the checks above read (map order is random).
	if got := run("(s.reset(), s.blocks().length)"); got != int64(0) {
		t.Errorf("after reset: %v blocks", got)
	}
}

func TestFirstBelow(t *testing.T) {
	vm := jsModule(t, "src/scroll.ts", "scroll")
	// 1000 items 30px high: the one under y=4510 is 150; reads are few.
	v, err := vm.RunString(`
var reads = 0;
var i = scroll.firstBelow(1000, function (i) { reads++; return (i + 1) * 30; }, 4510);
[i, reads, scroll.firstBelow(0, null, 5), scroll.firstBelow(3, function (i) { return (i + 1) * 10; }, 100)]
`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.Export().([]any)
	if got[0] != int64(150) || got[1].(int64) > 12 || got[2] != int64(-1) || got[3] != int64(2) {
		t.Fatalf("firstBelow: %v", got)
	}
}

// What extensions show: item/display updates an item in place (one not
// known is skipped), and an extText item's lines, diff tones and fold.
func TestExtensionDisplay(t *testing.T) {
	vm := jsModule(t, "src/transcript.ts", "tr")
	v, err := vm.RunString(`
var s = new tr.Transcript();
s.reset([{id: "r", type: "reasoning", text: "plan", blockId: "b"}]);
var b1 = s.blocks().slice();
s.display("r", {statuses: [{ext: "x", text: "translating"}], ext: "x", text: "PLAN"});
s.display("nope", {text: "x"});
var shown = s.get("r").display.text + "|" + s.get("r").text + "|" + (s.blocks()[0] !== b1[0]) + "|" + s.length;
s.display("r", null);
shown + "|" + s.get("r").display
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Export(); got != "PLAN|plan|true|1|null" {
		t.Fatalf("display: %v", got)
	}

	vm = jsModule(t, "src/exttext.ts", "et")
	for expr, want := range map[string]any{
		`et.lines("a\tb\r\n\u001b[31mred\u001b[0m\n x\n\n  \n").join("|")`: "a   b|red| x",
		`et.lines("").length`: int64(0),
		`["diff --git a/f b/f", "--- a/f", "+++ b/f", "@@ -1 +1 @@", "+new", "-old", " same", "== Staged =="].map(function (l) { return et.tone(l, "diff"); }).join(",")`: "meta,meta,meta,hunk,add,del,,",
		`et.tone("+not a diff", "")`:            "",
		`JSON.stringify(et.fold(30, 0, false))`: `{"shown":10,"hidden":20,"foldable":true}`,
		`JSON.stringify(et.fold(30, 5, true))`:  `{"shown":30,"hidden":0,"foldable":true}`,
		`JSON.stringify(et.fold(4, 5, false))`:  `{"shown":4,"hidden":0,"foldable":false}`,
	} {
		v, err := vm.RunString(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := v.Export(); got != want {
			t.Errorf("%s = %v, want %v", expr, got, want)
		}
	}
}
