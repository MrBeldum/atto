package ai

import "strings"

// Port of src/utils/text.ts.

// ContentText joins the text blocks of content.
func ContentText(content []Content, separator string) string {
	var parts []string
	for _, c := range content {
		if t, ok := c.(*TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, separator)
}

// GetSystemMessageText renders a system message as a complete prompt: its
// content followed by its sections.
func GetSystemMessageText(m *SystemMessage) string {
	var parts []string
	if m.Content != "" {
		parts = append(parts, m.Content)
	}
	for _, s := range m.Sections {
		if s.Text != nil && *s.Text != "" {
			parts = append(parts, *s.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// RenderSystemMessageUpdate renders a later system message for APIs that
// accept system messages mid-conversation.
func RenderSystemMessageUpdate(m *SystemMessage) string {
	var parts []string
	if m.Content != "" {
		parts = append(parts, m.Content)
	}
	for _, s := range m.Sections {
		if s.Text == nil {
			parts = append(parts, `Removed system prompt section "`+s.Name+`".`)
		} else {
			parts = append(parts, `Updated system prompt section "`+s.Name+"\":\n\n"+*s.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
