package app

import (
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/extensions"
)

// What extensions show in the terminal reaches /remote's clients as data:
// a block's statuses and replacement text (item/display, and on the item
// in thread/read), a text block (an extText item), status items and
// widgets (extension/ui, extensionUi) and notices (extension/notify).
func TestRemoteExtensionUI(t *testing.T) {
	a := remoteApp(t, newRemoteModel(t))
	runTurn(t, a, "first")
	var blockID, itemID string
	a.ui.Do(func() {
		for id, b := range a.blocks {
			blockID, itemID = id, b.display().item
		}
	})
	if blockID == "" || itemID == "" {
		t.Fatal("the answer has no block ID")
	}
	a.ui.Do(func() { a.cmdRemote("on") })
	c := a.remoteClient(t)
	read := c.must("thread/read", nil)
	eid, _ := read["eventId"].(float64)
	ev := c.events(eid)
	within(t, a, "the client to connect", func() bool { return a.remote.clients == 1 })

	h := newTUIHost(a)
	h.SetBlockStatus("tr", blockID, "translating…")
	h.SetBlockDisplay("tr", blockID, "**SHOWN**")
	h.SetBlockStatus("tr", "elsewhere", "ignored")
	h.SetStatus("tr", "mode", "on")
	h.SetWidget("tr", "w", []string{"one", "two"})
	h.ShowText("tr", "report", "+a\n-b", extensions.TextOptions{Lang: "diff", Preview: 1})
	h.Notify("tr", "hello", "warning")

	var display map[string]any
	for _, want := range []string{"item/display", "item/display", "extension/ui", "extension/ui", "item/completed", "extension/notify"} {
		m, _ := until(t, ev, want, nil)
		switch want {
		case "item/display":
			if m.Params["itemId"] != itemID || m.Params["blockId"] != blockID {
				t.Fatalf("item/display %v", m.Params)
			}
			display, _ = m.Params["display"].(map[string]any)
		case "item/completed":
			if it := item(m); it["type"] != "extText" || it["title"] != "report" || it["ext"] != "tr" || it["lang"] != "diff" || it["preview"] != float64(1) || it["text"] != "+a\n-b" {
				t.Fatalf("extText %v", it)
			}
		case "extension/notify":
			if m.Params["extension"] != "tr" || m.Params["message"] != "hello" || m.Params["level"] != "warning" {
				t.Fatalf("notify %v", m.Params)
			}
		}
	}
	if display["ext"] != "tr" || display["text"] != "**SHOWN**" || len(display["statuses"].([]any)) != 1 {
		t.Fatalf("display %v", display)
	}

	// thread/read has them too.
	read = c.must("thread/read", nil)
	var got []string
	for _, x := range read["items"].([]any) {
		it := x.(map[string]any)
		if it["blockId"] == blockID {
			d, _ := it["display"].(map[string]any)
			got = append(got, "display "+d["text"].(string))
		}
		if it["type"] == "extText" {
			got = append(got, "extText "+it["title"].(string))
		}
	}
	ui, _ := read["extensionUi"].(map[string]any)
	status, _ := ui["status"].([]any)
	widgets, _ := ui["widgets"].([]any)
	if strings.Join(got, ",") != "display **SHOWN**,extText report" || len(status) != 1 || len(widgets) != 1 ||
		status[0].(map[string]any)["key"] != "tr/mode" || status[0].(map[string]any)["text"] != "on" {
		t.Fatalf("thread/read: %v, ui %v", got, ui)
	}

	// Cleared: the clients hear it.
	h.ClearUI("tr")
	m, _ := until(t, ev, "extension/ui", func(m rmsg) bool {
		u, _ := m.Params["ui"].(map[string]any)
		s, _ := u["status"].([]any)
		w, _ := u["widgets"].([]any)
		return len(s) == 0 && len(w) == 0
	})
	if m.Params["ui"] == nil {
		t.Fatal("no ui")
	}
	h.SetBlockDisplay("tr", blockID, "")
	m, _ = until(t, ev, "item/display", nil)
	if d, _ := m.Params["display"].(map[string]any); d["text"] != nil {
		t.Fatalf("restored: %v", m.Params)
	}
}
