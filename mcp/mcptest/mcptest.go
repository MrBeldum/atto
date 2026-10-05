// Package mcptest is a fake MCP server for tests. A test binary serves it
// over stdio by calling ServeIfRequested first thing in TestMain, and
// starts it as a server by running itself (see Command).
package mcptest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Env is the environment variable that turns a test binary into the fake
// server.
const Env = "ATTO_TEST_MCP_SERVER"

// Command is the command (executable and no arguments) of a stdio server
// config that runs the fake server from the test binary.
func Command() (command string, env map[string]string) {
	exe, _ := os.Executable()
	return exe, map[string]string{Env: "1"}
}

// ServeIfRequested serves the fake server on stdio and exits when the
// process was started as one; otherwise it returns.
func ServeIfRequested() {
	if os.Getenv(Env) == "" {
		return
	}
	if err := NewServer().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type echoIn struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

type noIn struct{}

// NewServer builds the fake server. Its tools:
//
//	echo     returns its text
//	count    returns 1, 2, 3... for each call (a stateful tool)
//	pid      returns the server's process ID
//	fail     returns an error result
//	image    returns an image and text
//	env      returns the value of the environment variable named by "name"
func NewServer() *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "1"}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: "echo", Description: "Echo the text back.\nSecond line of the description."},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: in.Text}}}, nil, nil
		})
	var n atomic.Int64
	sdk.AddTool(s, &sdk.Tool{Name: "count", Description: "Count calls in this server process."},
		func(_ context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: itoa(n.Add(1))}}}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "pid", Description: "The server's process ID."},
		func(_ context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: itoa(int64(os.Getpid()))}}}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "fail", Description: "Always fails."},
		func(_ context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "it broke"}}}, nil, nil
		})
	sdk.AddTool(s, &sdk.Tool{Name: "image", Description: "An image and a caption."},
		func(_ context.Context, _ *sdk.CallToolRequest, _ noIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: "caption"},
				&sdk.ImageContent{Data: []byte("not really a png"), MIMEType: "image/png"},
			}}, nil, nil
		})
	type envIn struct {
		Name string `json:"name"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "env", Description: "An environment variable of the server."},
		func(_ context.Context, _ *sdk.CallToolRequest, in envIn) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: os.Getenv(in.Name)}}}, nil, nil
		})
	return s
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// HTTP serves a fake server over streamable HTTP for the life of the test
// and returns its URL. seen, if set, is called with each request's headers.
func HTTP(t testing.TB, seen func(http.Header)) string {
	t.Helper()
	srv := NewServer()
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen(r.Header)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}
