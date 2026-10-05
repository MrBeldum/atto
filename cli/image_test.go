package cli

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sebastianrcnt/atto/session"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withStdin points os.Stdin at a file holding data for the test.
func withStdin(t *testing.T, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestReadPromptInputImages(t *testing.T) {
	// An image on stdin is attached; the argument is the text.
	withStdin(t, pngBytes(t, 3, 2))
	text, imgs, err := ReadPromptInput([]string{"what", "is", "this?"}, nil)
	if err != nil || text != "what is this?\n\n[image 1: 3x2 PNG]" || len(imgs) != 1 || imgs[0].MIME != "image/png" || len(imgs[0].Data) == 0 {
		t.Fatalf("stdin image: %q, %d images, %v", text, len(imgs), err)
	}

	// -image files come first; text on stdin is still appended.
	file := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(file, pngBytes(t, 5, 4), 0o644); err != nil {
		t.Fatal(err)
	}
	withStdin(t, []byte("some logs\n"))
	text, imgs, err = ReadPromptInput([]string{"explain"}, []string{file})
	if err != nil || text != "explain\n\nsome logs\n\n[image 1: 5x4 PNG]" || len(imgs) != 1 {
		t.Fatalf("-image: %q, %d images, %v", text, len(imgs), err)
	}

	// An image alone is a prompt too.
	withStdin(t, nil)
	if text, imgs, err = ReadPromptInput(nil, []string{file, file}); err != nil || text != "[image 1: 5x4 PNG]\n[image 2: 5x4 PNG]" || len(imgs) != 2 {
		t.Fatalf("images only: %q, %d images, %v", text, len(imgs), err)
	}

	// Not an image file; a broken image on stdin.
	notImage := filepath.Join(t.TempDir(), "notes.txt")
	_ = os.WriteFile(notImage, []byte("hello"), 0o644)
	if _, _, err := ReadPromptInput([]string{"x"}, []string{notImage}); err == nil || !strings.Contains(err.Error(), "-image "+notImage) {
		t.Fatalf("bad -image: %v", err)
	}
	withStdin(t, []byte("\x89PNG\r\n\x1a\ntruncated"))
	if _, _, err := ReadPromptInput([]string{"x"}, nil); err == nil || !strings.Contains(err.Error(), "image on stdin") {
		t.Fatalf("broken stdin image: %v", err)
	}
}

func TestSniffFormats(t *testing.T) {
	for _, c := range []struct {
		data string
		want bool
	}{
		{"\x89PNG\r\n\x1a\n....", true},
		{"\xff\xd8\xff\xe0", true},
		{"GIF89a..", true},
		{"GIF87a..", true},
		{"RIFF\x10\x00\x00\x00WEBPVP8 ", true},
		{"RIFF\x10\x00\x00\x00WAVEfmt ", false},
		{"diff --git a/x b/x", false},
		{"", false},
	} {
		withStdin(t, []byte(c.data))
		if got := sniffed(t); got != c.want {
			t.Errorf("%q: sniffed %v", c.data, got)
		}
	}
}

// sniffed reports whether ReadPromptInput took stdin for an image (a
// valid one or not).
func sniffed(t *testing.T) bool {
	_, imgs, err := ReadPromptInput([]string{"p"}, nil)
	return len(imgs) > 0 || (err != nil && strings.Contains(err.Error(), "image on stdin"))
}

// imageModelServer is a chat completions server that keeps request bodies.
func imageModelServer(t *testing.T, input string) func() []string {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a gray square\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("ATTO_DIR", dir)
	models := `{"providers":{"fake":{"baseUrl":"` + srv.URL + `","models":[{"id":"m","contextWindow":10000,"input":` + input + `}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), bodies...) }
}

// quiet sends the run's stdout and stderr nowhere.
func quiet(t *testing.T) {
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	out, errOut := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	t.Cleanup(func() { os.Stdout, os.Stderr = out, errOut; null.Close() })
}

func TestRunPrintImages(t *testing.T) {
	bodies := imageModelServer(t, `["text","image"]`)
	t.Chdir(t.TempDir())
	withStdin(t, pngBytes(t, 3, 2))
	text, imgs, err := ReadPromptInput([]string{"what is this?"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	quiet(t)
	if err := RunPrint(PrintOptions{Prompt: text, Images: imgs}); err != nil {
		t.Fatal(err)
	}
	b := bodies()
	if len(b) != 1 || !strings.Contains(b[0], "data:image/png;base64,") || !strings.Contains(b[0], `[image 1: 3x2 PNG]`) {
		t.Fatalf("request: %v", b)
	}
	// The session refers to the stored image.
	cwd, _ := os.Getwd()
	s, ok := session.Latest(cwd)
	if !ok {
		t.Fatal("no session")
	}
	_, entries, _ := session.Load(s.Path)
	var saved bool
	for _, e := range entries {
		if m := e.Message; m != nil && m.Role == "user" && len(m.Images) == 1 {
			_, err := os.Stat(filepath.Join(os.Getenv("ATTO_DIR"), "images", m.Images[0].File))
			saved = err == nil
		}
	}
	if !saved {
		t.Fatal("image not stored with the session")
	}
}

func TestRunPrintRejectsImagesForTextModel(t *testing.T) {
	bodies := imageModelServer(t, `["text"]`)
	t.Chdir(t.TempDir())
	withStdin(t, pngBytes(t, 3, 2))
	text, imgs, err := ReadPromptInput([]string{"what is this?"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	quiet(t)
	err = RunPrint(PrintOptions{Prompt: text, Images: imgs})
	if err == nil || !strings.Contains(err.Error(), "does not support image input") || !strings.Contains(err.Error(), "(-m)") {
		t.Fatalf("err %v", err)
	}
	if len(bodies()) != 0 {
		t.Fatal("nothing should be sent")
	}
}
