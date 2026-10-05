package images

import (
	"context"

	"github.com/sebastianrcnt/atto/clipboard"
	"github.com/sebastianrcnt/atto/provider"
)

// ErrNoImage means the clipboard holds no image.
var ErrNoImage = clipboard.ErrNoImage

// FromClipboard reads the clipboard image through c and prepares it for the
// model. Clipboard tools may hand back text, so a tool's output only counts
// when Prepare accepts it.
func FromClipboard(ctx context.Context, c clipboard.Clipboard) (provider.Image, error) {
	var im provider.Image
	_, err := c.ReadImage(ctx, func(data []byte) (err error) {
		im, err = Prepare(data)
		return err
	})
	return im, err
}

// SystemClipboardImage reads the image on the system clipboard.
func SystemClipboardImage(ctx context.Context) (provider.Image, error) {
	return FromClipboard(ctx, clipboard.System())
}
