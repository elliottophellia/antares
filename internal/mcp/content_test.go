package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// helperClient connects to the in-process stdio fixture (see TestHelperServer).
func helperClient(t *testing.T) *Client {
	t.Helper()
	client, err := Connect(context.Background(), "fake", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env:       map[string]string{"ANTARES_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestCallContent drives Call over the tool result shapes a server may legally
// return. A result the client cannot represent must say so rather than pass for
// an empty success: the model would otherwise proceed believing the tool ran and
// had nothing to report.
func TestCallContent(t *testing.T) {
	client := helperClient(t)

	cases := []struct {
		name string
		raw  string
		// wantErr is a substring the error must name; empty means no error.
		wantErr  string
		wantText string
	}{
		{
			name:     "text",
			raw:      `{"content":[{"type":"text","text":"PLAIN"}]}`,
			wantText: "PLAIN",
		},
		{
			name:     "embedded resource text",
			raw:      `{"content":[{"type":"resource","resource":{"uri":"file:///a","mimeType":"text/plain","text":"HELLO"}}]}`,
			wantText: "HELLO",
		},
		{
			name:     "embedded resource blob",
			raw:      `{"content":[{"type":"resource","resource":{"uri":"file:///a.png","mimeType":"image/png","blob":"QUJDRA=="}}]}`,
			wantText: "[resource: file:///a.png (image/png), 8 bytes base64]",
		},
		{
			name:     "image",
			raw:      `{"content":[{"type":"image","mimeType":"image/png","data":"QUJDRA=="}]}`,
			wantText: "[image: image/png, 8 bytes base64]",
		},
		{
			name:    "audio only",
			raw:     `{"content":[{"type":"audio","data":"QUJDRA==","mimeType":"audio/wav"}]}`,
			wantErr: "audio",
		},
		{
			name:    "unknown type only",
			raw:     `{"content":[{"type":"hologram","data":"QUJDRA=="}]}`,
			wantErr: "hologram",
		},
		{
			name:    "resource without a payload",
			raw:     `{"content":[{"type":"resource"}]}`,
			wantErr: "resource",
		},
		{
			name:     "text alongside audio",
			raw:      `{"content":[{"type":"text","text":"HELLO"},{"type":"audio","data":"QUJDRA==","mimeType":"audio/wav"}]}`,
			wantText: "HELLO",
		},
		{
			name:     "empty content",
			raw:      `{"content":[]}`,
			wantText: "(no content returned)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := client.Call(ctx, "raw", map[string]any{"result": tc.raw})
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Call returned %+v with no error, want one naming %q", res, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to name %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if res.Text != tc.wantText {
				t.Fatalf("text = %q, want %q", res.Text, tc.wantText)
			}
			if res.IsError {
				t.Fatalf("IsError = true for %q, want false", res.Text)
			}
		})
	}
}

// TestCallErrorResultKeepsItsText checks that a server-flagged error still comes
// back as a result rather than a transport error, since its content is the
// explanation the model needs.
func TestCallErrorResultKeepsItsText(t *testing.T) {
	client := helperClient(t)
	res, err := client.Call(context.Background(), "raw",
		map[string]any{"result": `{"isError":true,"content":[{"type":"text","text":"file not found"}]}`})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError || res.Text != "file not found" {
		t.Fatalf("result = %+v, want the error text carried through", res)
	}
}

// TestReadResourceEmptyContentsIsNotFound pins that a server answering with no
// contents is a miss, not an empty document: Manager.ReadResource asks every
// server in turn and must keep looking rather than accept the first empty reply.
func TestReadResourceEmptyContentsIsNotFound(t *testing.T) {
	client := helperClient(t)

	text, err := client.ReadResource(context.Background(), `raw:{"contents":[]}`)
	if err == nil {
		t.Fatalf("ReadResource returned %q with no error, want a not-found error", text)
	}

	text, err = client.ReadResource(context.Background(),
		`raw:{"contents":[{"uri":"mem:///a","mimeType":"text/plain","text":"BODY"}]}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if text != "BODY" {
		t.Fatalf("text = %q, want %q", text, "BODY")
	}
}

// TestManagerReadResourceRejectsEmptyAnswer is the same rule seen from the
// manager: with one server that has nothing, the read fails instead of handing
// the model a blank document.
func TestManagerReadResourceRejectsEmptyAnswer(t *testing.T) {
	manager := NewManager()
	manager.Connect(context.Background(), helperConfig("online"))
	defer manager.Close()

	text, err := manager.ReadResource(context.Background(), `raw:{"contents":[]}`)
	if err == nil {
		t.Fatalf("ReadResource returned %q with no error, want the search to fail", text)
	}
}
