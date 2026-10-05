package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server statuses, as Info.Status.
const (
	NotStarted    = "not started"
	Running       = "running"
	Failed        = "failed"
	NeedsApproval = "needs approval"
	DeniedStatus  = "denied"
)

// Info describes a configured server, for atto mcp list and the Loaded
// block.
type Info struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Transport string `json:"transport"`
	Target    string `json:"target"` // the command line or the URL
	Path      string `json:"path"`   // the file that defines it
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	// Tools is the number of tools once the server has been started;
	// -1 before that.
	Tools int `json:"tools"`
	// Invalid: the entry itself is wrong (Error says how), as opposed to a
	// start that failed.
	Invalid bool   `json:"invalid,omitempty"`
	Hash    string `json:"hash,omitempty"` // of the entry as written
}

// ToolInfo describes a tool a server offers.
type ToolInfo struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Content is one piece of a tool's result. Text is the text itself, or a
// note for what is not text ("image: image/png, 2048 bytes").
type Content struct {
	Type     string `json:"type"` // text, image, audio, resource_link or resource
	Text     string `json:"text,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
}

// Result is what a tool call returned.
type Result struct {
	// Text is the result as the model reads it: text content as it is,
	// the rest summarized in brackets.
	Text    string    `json:"text"`
	IsError bool      `json:"isError,omitempty"`
	Content []Content `json:"content,omitempty"`
	// Structured is the tool's structured output, if it had any.
	Structured any `json:"structured,omitempty"`
}

// summarize turns the SDK's result into a Result.
func summarize(r *sdk.CallToolResult) Result {
	out := Result{IsError: r.IsError, Structured: r.StructuredContent}
	var lines []string
	for _, c := range r.Content {
		var x Content
		switch c := c.(type) {
		case *sdk.TextContent:
			x = Content{Type: "text", Text: c.Text}
			lines = append(lines, c.Text)
		case *sdk.ImageContent:
			x = Content{Type: "image", MIMEType: c.MIMEType, Bytes: len(c.Data)}
			x.Text = fmt.Sprintf("[image: %s, %d bytes]", c.MIMEType, len(c.Data))
			lines = append(lines, x.Text)
		case *sdk.AudioContent:
			x = Content{Type: "audio", MIMEType: c.MIMEType, Bytes: len(c.Data)}
			x.Text = fmt.Sprintf("[audio: %s, %d bytes]", c.MIMEType, len(c.Data))
			lines = append(lines, x.Text)
		case *sdk.ResourceLink:
			x = Content{Type: "resource_link", URI: c.URI, MIMEType: c.MIMEType}
			x.Text = "[resource link: " + c.URI + "]"
			lines = append(lines, x.Text)
		case *sdk.EmbeddedResource:
			res := c.Resource
			if res == nil {
				continue
			}
			x = Content{Type: "resource", URI: res.URI, MIMEType: res.MIMEType, Bytes: len(res.Blob)}
			if res.Text != "" {
				x.Text = res.Text
			} else {
				x.Text = fmt.Sprintf("[resource: %s, %s, %d bytes]", res.URI, orUnknown(res.MIMEType), len(res.Blob))
			}
			lines = append(lines, x.Text)
		default:
			x = Content{Type: "unknown", Text: "[unsupported content]"}
			lines = append(lines, x.Text)
		}
		out.Content = append(out.Content, x)
	}
	out.Text = strings.Join(lines, "\n")
	if out.Text == "" && r.StructuredContent != nil {
		if b, err := json.MarshalIndent(r.StructuredContent, "", "  "); err == nil {
			out.Text = string(b)
		}
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown type"
	}
	return s
}
