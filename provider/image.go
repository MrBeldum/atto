package provider

import "encoding/base64"

// Image is an image attached to a user message. Sessions store only the
// reference (File, MIME, size); the bytes live in a content-addressed file
// (package images) and are loaded into Data before a request. Because the
// file never changes, the data URL built from it is byte-identical on every
// request, which keeps the prefix cache warm.
type Image struct {
	File   string `json:"file"` // name in the images directory, e.g. "<sha256>.png"
	MIME   string `json:"mime"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Data   []byte `json:"-"`
}

// ImageMissing is sent in place of an image whose file could not be read.
const ImageMissing = "[image unavailable: its file is missing]"

// dataURL returns the image as a data URL, or "" when its bytes are not loaded.
func (im Image) dataURL() string {
	if len(im.Data) == 0 {
		return ""
	}
	return "data:" + im.MIME + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
}

// chatMessages prepares messages for a chat completions server: it drops
// Responses-only state and turns messages with images into content parts.
// Messages without images marshal exactly as before.
func chatMessages(msgs []Message) []any {
	out := make([]any, len(msgs))
	for i, m := range msgs {
		m.Reasoning = nil
		if len(m.Images) == 0 {
			out[i] = m
			continue
		}
		parts := []map[string]any{}
		if m.Content != "" {
			parts = append(parts, map[string]any{"type": "text", "text": m.Content})
		}
		for _, im := range m.Images {
			if u := im.dataURL(); u != "" {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
			} else {
				parts = append(parts, map[string]any{"type": "text", "text": ImageMissing})
			}
		}
		out[i] = chatImageMessage{Role: m.Role, Content: parts}
	}
	return out
}

type chatImageMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

// responsesUserContent builds the content parts of a Responses user message.
func responsesUserContent(m Message) []map[string]any {
	parts := []map[string]any{}
	if m.Content != "" || len(m.Images) == 0 {
		parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
	}
	for _, im := range m.Images {
		if u := im.dataURL(); u != "" {
			parts = append(parts, map[string]any{"type": "input_image", "image_url": u})
		} else {
			parts = append(parts, map[string]any{"type": "input_text", "text": ImageMissing})
		}
	}
	return parts
}
