package images

import (
	"context"
	"errors"
	"testing"

	"github.com/sebastianrcnt/atto/clipboard"
)

func TestFromClipboard(t *testing.T) {
	img := pngBytes(t, 4, 2)
	fake := func(out []byte) clipboard.Clipboard {
		return clipboard.Clipboard{
			GOOS:     "linux",
			Getenv:   func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] },
			LookPath: func(n string) (string, error) { return "/bin/" + n, nil },
			Run:      func(context.Context, string, ...string) ([]byte, error) { return out, nil },
		}
	}
	im, err := FromClipboard(context.Background(), fake(img))
	if err != nil || im.Width != 4 || im.Height != 2 {
		t.Fatalf("%+v %v", im, err)
	}
	// Text on the clipboard is not an image.
	if _, err := FromClipboard(context.Background(), fake([]byte("hello"))); !errors.Is(err, ErrNoImage) {
		t.Fatalf("text: %v", err)
	}
}
