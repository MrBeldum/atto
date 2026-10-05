package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testImage = Image{File: "abc.png", MIME: "image/png", Width: 2, Height: 1, Data: []byte("PNGDATA")}

const testDataURL = "data:image/png;base64,UE5HREFUQQ=="

// captureBody runs one request against a server that records the body.
func captureBody(t *testing.T, s func(url string) Streamer, req Request) []byte {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	if _, err := s(srv.URL).Stream(context.Background(), req, Handler{}); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestChatCompletionsImageParts(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "look [image 1: 2x1 PNG]", Images: []Image{testImage, {File: "gone.png", MIME: "image/png"}}},
		{Role: "user", Content: "plain"},
	}
	body := captureBody(t, func(u string) Streamer { return &Client{BaseURL: u} }, Request{Model: "m", Messages: msgs})
	var b struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	want := `{"role":"user","content":[{"text":"look [image 1: 2x1 PNG]","type":"text"},` +
		`{"image_url":{"url":"` + testDataURL + `"},"type":"image_url"},` +
		`{"text":"` + ImageMissing + `","type":"text"}]}`
	if string(b.Messages[1]) != want {
		t.Fatalf("image message\n got %s\nwant %s", b.Messages[1], want)
	}
	// Text-only messages keep their plain string form, and nothing about
	// the session reference (file name) reaches the server.
	if string(b.Messages[2]) != `{"role":"user","content":"plain"}` || bytes.Contains(body, []byte("abc.png")) {
		t.Fatalf("plain message %s / body %s", b.Messages[2], body)
	}
}

func TestResponsesImageParts(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Images: []Image{testImage}},
	}
	body := captureBody(t, func(u string) Streamer { return &ResponsesClient{BaseURL: u} }, Request{Model: "m", Messages: msgs})
	var b struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	want := `{"content":[{"image_url":"` + testDataURL + `","type":"input_image"}],"role":"user"}`
	if len(b.Input) != 1 || string(b.Input[0]) != want {
		t.Fatalf("input %s\nwant %s", b.Input, want)
	}
	if strings.Contains(string(body), `"images"`) {
		t.Fatalf("session field leaked: %s", body)
	}
}

// The request bytes for an image message must not change between requests,
// or the prefix cache would miss on every turn after it.
func TestImageRequestIsStable(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "x", Images: []Image{testImage}}}
	c := &Client{}
	a, _ := c.body(Request{Model: "m", Messages: msgs})
	b, _ := c.body(Request{Model: "m", Messages: append(msgs, Message{Role: "assistant", Content: "y"})})
	if !bytes.HasPrefix(b, a[:bytes.LastIndex(a, []byte("]"))]) {
		t.Fatalf("prefix changed:\n%s\n%s", a, b)
	}
	// The stored form keeps only the reference.
	j, _ := json.Marshal(msgs[0])
	if string(j) != `{"role":"user","content":"x","images":[{"file":"abc.png","mime":"image/png","width":2,"height":1}]}` {
		t.Fatalf("stored form %s", j)
	}
}
