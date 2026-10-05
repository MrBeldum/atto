package images

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sebastianrcnt/atto/provider"
)

// Sniff reports whether data starts like an image atto accepts: PNG,
// JPEG, GIF or WebP, by their magic bytes. It is how atto -p tells an
// image on stdin from text.
func Sniff(data []byte) bool {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(data, []byte("\xff\xd8\xff")),
		bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return true
	}
	return false
}

// Placeholder is the text that stands for the n-th image (1-based) of a
// message, as the TUI's editor shows it: "[image 1: 1024x768 PNG]".
func Placeholder(n int, im provider.Image) string {
	return fmt.Sprintf("[image %d: %s]", n, Label(im))
}

// WithPlaceholders adds a placeholder line per image after text, for
// messages whose images were attached outside the TUI's editor (atto -p,
// the server). The model then sees the same text a TUI message carries,
// and it still says what the images were once compaction drops them.
func WithPlaceholders(text string, imgs []provider.Image) string {
	if len(imgs) == 0 {
		return text
	}
	lines := make([]string, len(imgs))
	for i, im := range imgs {
		lines[i] = Placeholder(i+1, im)
	}
	if text = strings.TrimRight(text, " \t\r\n"); text != "" {
		text += "\n\n"
	}
	return text + strings.Join(lines, "\n")
}

// Unsupported says that model takes no image input. switchHint is how to
// pick another model where it is shown ("/model", "-m").
func Unsupported(model, switchHint, modelsPath string) string {
	return fmt.Sprintf("Model %s does not support image input. Remove the images or switch models (%s). "+
		`If it does, add "input": ["text", "image"] to it in %s.`, model, switchHint, modelsPath)
}
