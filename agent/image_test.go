package agent

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/images"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
)

func testPNG(t *testing.T) provider.Image {
	t.Helper()
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 3, 2)))
	im, err := images.Prepare(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func imageAgent(url string, input ...string) *Agent {
	return New(config.ModelRef{ProviderName: "t", Provider: config.Provider{BaseURL: url},
		Model: config.Model{ID: "m", ContextWindow: 100000, Input: input}}, "", os.TempDir())
}

// contentParts returns the parts of a chat completions message, or nil
// when its content is a plain string.
func contentParts(m map[string]any) []map[string]any {
	raw, _ := m["content"].([]any)
	var out []map[string]any
	for _, p := range raw {
		out = append(out, p.(map[string]any))
	}
	return out
}

func TestImagesSessionCompactionAndRestore(t *testing.T) {
	t.Setenv("ATTO_DIR", t.TempDir())
	im := testPNG(t)
	if err := images.Save(im); err != nil {
		t.Fatal(err)
	}
	srv, seen := fakeServer(t, text("a cat"), text("NOTES"), text("ok"))
	a := imageAgent(srv.URL, "text", "image")
	w := session.New(t.TempDir())
	a.Record = w.Append
	label := "[image 1: " + images.Label(im) + "]"
	if err := a.RunWithImages(context.Background(), "what is "+label, []provider.Image{im}, func(any) {}); err != nil {
		t.Fatal(err)
	}
	parts := contentParts(seen()[0][1])
	if len(parts) != 2 || parts[1]["type"] != "image_url" ||
		!strings.HasPrefix(parts[1]["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,") {
		t.Fatalf("first request user message: %v", seen()[0][1])
	}

	// The session file holds the reference, not the bytes.
	w.Close()
	data, _ := os.ReadFile(w.Path)
	if !bytes.Contains(data, []byte(`"file":"`+im.File+`"`)) || bytes.Contains(data, []byte("base64")) {
		t.Fatalf("session line: %s", data)
	}
	_, entries, err := session.Load(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	got := entries[0].Message.Images
	if len(got) != 1 || got[0].File != im.File || got[0].Width != 3 || got[0].Data != nil {
		t.Fatalf("loaded refs %+v", got)
	}

	// Resume: the bytes come back from the image store, and the replayed
	// request is identical to the original one.
	b := imageAgent(srv.URL, "text", "image")
	b.Restore(entries)
	_, r1 := a.request()
	_, r2 := b.request()
	if !reflect.DeepEqual(r1.Messages, r2.Messages) {
		t.Fatalf("restored request differs:\n%+v\n%+v", r1.Messages, r2.Messages)
	}

	// Compaction keeps the user message as text with a note.
	if err := b.Compact(context.Background(), func(any) {}); err != nil {
		t.Fatal(err)
	}
	kept := b.messages[0]
	if kept.Role != "user" || len(kept.Images) != 0 || kept.Content != "what is "+label+"\n"+ImagesCompacted {
		t.Fatalf("kept after compaction: %+v", kept)
	}
	// The compaction request itself still carried the image (same prefix).
	if parts := contentParts(seen()[1][1]); len(parts) != 2 {
		t.Fatalf("compaction request: %v", seen()[1][1])
	}

	// A missing image file is sent as a note instead of failing.
	os.Remove(filepath.Join(images.Dir(), im.File))
	c := imageAgent(srv.URL, "text", "image")
	c.Restore(entries)
	_, r3 := c.request()
	if ims := r3.Messages[1].Images; len(ims) != 1 || ims[0].Data != nil {
		t.Fatalf("missing image: %+v", ims)
	}
}

func TestImagesOmittedForTextModel(t *testing.T) {
	srv, seen := fakeServer(t, text("ok"))
	a := imageAgent(srv.URL) // no "image" input
	im := testPNG(t)
	if err := a.RunWithImages(context.Background(), "see", []provider.Image{im}, func(any) {}); err != nil {
		t.Fatal(err)
	}
	if got := seen()[0][1]["content"]; got != "see\n"+ImagesUnsupported {
		t.Fatalf("sent %v", got)
	}
	if len(a.messages[0].Images) != 1 {
		t.Fatal("history lost the image")
	}
}
