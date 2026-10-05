package extensions

import (
	"slices"
	"strings"
)

// UIState is what extensions show besides the transcript: status line items
// (SetStatus) and widgets, bands of lines above the input (SetWidget), each
// under "<extension>/<key>" in the order they were first set. A front end
// keeps one and draws it; it is not safe for concurrent use.
type UIState struct {
	statusKeys []string
	status     map[string]string
	widgetKeys []string
	widgets    map[string][]string
}

// UIItem is a status item (Text) or a widget (Lines) under its key.
type UIItem struct {
	Key   string
	Text  string
	Lines []string
}

// SetStatus sets (text "" removes) the status item key.
func (u *UIState) SetStatus(key, text string) {
	if u.status == nil {
		u.status = map[string]string{}
	}
	if text == "" {
		delete(u.status, key)
		u.statusKeys = slices.DeleteFunc(u.statusKeys, func(k string) bool { return k == key })
		return
	}
	if _, ok := u.status[key]; !ok {
		u.statusKeys = append(u.statusKeys, key)
	}
	u.status[key] = text
}

// SetWidget sets (nil removes) the widget key.
func (u *UIState) SetWidget(key string, lines []string) {
	if u.widgets == nil {
		u.widgets = map[string][]string{}
	}
	if lines == nil {
		delete(u.widgets, key)
		u.widgetKeys = slices.DeleteFunc(u.widgetKeys, func(k string) bool { return k == key })
		return
	}
	if _, ok := u.widgets[key]; !ok {
		u.widgetKeys = append(u.widgetKeys, key)
	}
	u.widgets[key] = lines
}

// Clear removes every status item and widget of ext.
func (u *UIState) Clear(ext string) {
	for _, k := range slices.Clone(u.statusKeys) {
		if strings.HasPrefix(k, ext+"/") {
			u.SetStatus(k, "")
		}
	}
	for _, k := range slices.Clone(u.widgetKeys) {
		if strings.HasPrefix(k, ext+"/") {
			u.SetWidget(k, nil)
		}
	}
}

// Status is the status items in order.
func (u *UIState) Status() []UIItem {
	out := make([]UIItem, 0, len(u.statusKeys))
	for _, k := range u.statusKeys {
		out = append(out, UIItem{Key: k, Text: u.status[k]})
	}
	return out
}

// Widgets is the widgets in order.
func (u *UIState) Widgets() []UIItem {
	out := make([]UIItem, 0, len(u.widgetKeys))
	for _, k := range u.widgetKeys {
		out = append(out, UIItem{Key: k, Lines: u.widgets[k]})
	}
	return out
}

// Empty reports whether nothing is shown.
func (u *UIState) Empty() bool { return len(u.statusKeys) == 0 && len(u.widgetKeys) == 0 }
