package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"time"
)

func TestTurnWithImages(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"gray\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	t.Setenv("ATTO_DIR", dir)
	os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"fake":{"baseUrl":"`+srv.URL+`","models":[`+
		`{"id":"img","contextWindow":10000,"input":["text","image"]},{"id":"txt","contextWindow":10000}]}}}`), 0o644)
	s := New("test", t.TempDir())
	t.Cleanup(s.Close)

	var b bytes.Buffer
	png.Encode(&b, image.NewGray(image.Rect(0, 0, 3, 2)))
	data := base64.StdEncoding.EncodeToString(b.Bytes())
	fail := func(params map[string]any, want string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "turn/start", "params": params})
		resp := s.Handle(context.Background(), raw)
		if resp.Error == nil || resp.Error.Code != codeInvalidParams || !strings.Contains(resp.Error.Message, want) {
			t.Fatalf("%v: want an error with %q, got %+v", params["images"], want, resp.Error)
		}
	}

	txt := call(t, s, "thread/start", map[string]any{"model": "fake/txt"})["threadId"]
	fail(map[string]any{"threadId": txt, "input": "see", "images": []any{map[string]string{"mimeType": "image/png", "data": data}}}, "does not support image input")

	id := call(t, s, "thread/start", map[string]any{"model": "fake/img"})["threadId"]
	img := func(mime, data string) []any { return []any{map[string]string{"mimeType": mime, "data": data}} }
	fail(map[string]any{"threadId": id, "input": "x", "images": img("image/tiff", data)}, "not supported")
	fail(map[string]any{"threadId": id, "input": "x", "images": img("image/png", "!!!")}, "not base64")
	fail(map[string]any{"threadId": id, "input": "x", "images": img("image/png", base64.StdEncoding.EncodeToString([]byte("hello")))}, "not a PNG")
	fail(map[string]any{"threadId": id, "input": "x", "images": img("image/png", strings.Repeat("A", 16<<20))}, "larger than 10 MB")
	var many []any
	for range 11 {
		many = append(many, map[string]string{"mimeType": "image/png", "data": data})
	}
	fail(map[string]any{"threadId": id, "input": "x", "images": many}, "too many images")

	// A data: URL works too, and an image needs no text.
	call(t, s, "turn/start", map[string]any{"threadId": id, "images": img("", "data:image/png;base64,"+data)})
	var r map[string]any
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if r = call(t, s, "thread/read", map[string]any{"threadId": id}); r["busy"] == false {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turn did not finish")
		}
	}
	if got := itemTexts(r); got != "[image 1: 3x2 PNG];gray;" {
		t.Fatalf("items %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "data:image/png;base64,"+data) {
		t.Fatalf("model request %v", bodies)
	}
	files, _ := os.ReadDir(filepath.Join(dir, "images"))
	if len(files) != 1 {
		t.Fatalf("stored images %v", files)
	}
}
