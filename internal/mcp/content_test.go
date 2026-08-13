package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
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
// had nothing to report. A result it can represent that happens to be empty —
// an empty file is the everyday one — is not that, and must not be an error.
func TestCallContent(t *testing.T) {
	client := helperClient(t)

	cases := []struct {
		name string
		raw  string
		// wantErr is a substring the error must contain; empty means no error.
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
			name:     "embedded resource holding an empty file",
			raw:      `{"content":[{"type":"resource","resource":{"uri":"file:///empty","mimeType":"text/plain","text":""}}]}`,
			wantText: "(no content returned)",
		},
		{
			name:     "embedded resource blob",
			raw:      `{"content":[{"type":"resource","resource":{"uri":"file:///a.png","mimeType":"image/png","blob":"QUJDRA=="}}]}`,
			wantText: "[resource: file:///a.png (image/png), 8 bytes base64]",
		},
		{
			name:     "embedded resource holding an empty binary file",
			raw:      `{"content":[{"type":"resource","resource":{"uri":"file:///empty.bin","mimeType":"application/octet-stream","blob":""}}]}`,
			wantText: "(no content returned)",
		},
		{
			name:     "image",
			raw:      `{"content":[{"type":"image","mimeType":"image/png","data":"QUJDRA=="}]}`,
			wantText: "[image: image/png, 8 bytes base64]",
		},
		{
			name:    "image with no data",
			raw:     `{"content":[{"type":"image","mimeType":"image/png"}]}`,
			wantErr: "cannot represent: image with no data",
		},
		{
			name:    "audio only",
			raw:     `{"content":[{"type":"audio","data":"QUJDRA==","mimeType":"audio/wav"}]}`,
			wantErr: "cannot represent: audio",
		},
		{
			name:    "unknown type only",
			raw:     `{"content":[{"type":"hologram","data":"QUJDRA=="}]}`,
			wantErr: "cannot represent: hologram",
		},
		{
			name:    "resource with no payload key",
			raw:     `{"content":[{"type":"resource"}]}`,
			wantErr: "cannot represent: resource with no text or blob",
		},
		{
			name:    "resource with an empty payload object",
			raw:     `{"content":[{"type":"resource","resource":{"uri":"file:///a"}}]}`,
			wantErr: "cannot represent: resource with no text or blob",
		},
		{
			name:    "a repeated kind is named once",
			raw:     `{"content":[{"type":"audio","data":"a"},{"type":"hologram"},{"type":"audio","data":"b"}]}`,
			wantErr: "cannot represent: audio, hologram",
		},
		{
			name:     "text alongside audio",
			raw:      `{"content":[{"type":"text","text":"HELLO"},{"type":"audio","data":"QUJDRA==","mimeType":"audio/wav"}]}`,
			wantText: "HELLO",
		},
		{
			name:     "text alongside an empty resource",
			raw:      `{"content":[{"type":"text","text":"HELLO"},{"type":"resource","resource":{"uri":"file:///empty","text":""}}]}`,
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
					t.Fatalf("Call returned %+v with no error, want one saying %q", res, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to say %q", err, tc.wantErr)
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

// TestCallErrorBoundsWhatTheServerNamed keeps a server from writing the error
// message: the kinds it names are the server's own strings, so each is cut to
// contentKindChars and no more than maxContentKinds of them are listed.
func TestCallErrorBoundsWhatTheServerNamed(t *testing.T) {
	client := helperClient(t)

	items := []string{fmt.Sprintf(`{"type":%q}`, strings.Repeat("z", 5000))}
	for i := 0; i < 8; i++ {
		items = append(items, fmt.Sprintf(`{"type":"kind-%d"}`, i))
	}

	_, err := client.Call(context.Background(), "raw",
		map[string]any{"result": `{"content":[` + strings.Join(items, ",") + `]}`})
	if err == nil {
		t.Fatal("expected an error naming the content this client cannot represent")
	}
	msg := err.Error()
	if strings.Contains(msg, strings.Repeat("z", contentKindChars+1)) {
		t.Fatalf("a server-supplied type reached the error untruncated: %s", msg)
	}
	// Nine distinct kinds arrived and five are named.
	if !strings.Contains(msg, "and 4 more") {
		t.Fatalf("error = %s, want it to count the kinds it did not name", msg)
	}
	if len(msg) > 400 {
		t.Fatalf("error is %d bytes, want a bounded message: %s", len(msg), msg)
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

// TestReadResourcePayloads is the other half of that rule: a resource that is
// present but empty reads as an empty document, never as a miss and never as an
// error.
func TestReadResourcePayloads(t *testing.T) {
	client := helperClient(t)

	cases := []struct {
		name     string
		contents string
		wantText string
	}{
		{
			name:     "text",
			contents: `{"uri":"mem:///a","mimeType":"text/plain","text":"BODY"}`,
			wantText: "BODY",
		},
		{
			name:     "an empty file",
			contents: `{"uri":"mem:///a","mimeType":"text/plain","text":""}`,
			wantText: "(resource is empty)",
		},
		{
			name:     "blob",
			contents: `{"uri":"mem:///a.bin","mimeType":"application/octet-stream","blob":"QUJDRA=="}`,
			wantText: "[resource: mem:///a.bin (application/octet-stream), 8 bytes base64]",
		},
		{
			name:     "an empty binary file",
			contents: `{"uri":"mem:///a.bin","mimeType":"application/octet-stream","blob":""}`,
			wantText: "(resource is empty)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, err := client.ReadResource(context.Background(), `raw:{"contents":[`+tc.contents+`]}`)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if text != tc.wantText {
				t.Fatalf("text = %q, want %q", text, tc.wantText)
			}
		})
	}
}

// TestManagerReadResourceRejectsEmptyAnswer is the client rule seen from the
// manager: with one server that has nothing, the read fails instead of handing
// the model a blank document.
func TestManagerReadResourceRejectsEmptyAnswer(t *testing.T) {
	manager := NewManager()
	manager.Connect(context.Background(), helperConfig("no-resources"))
	defer manager.Close()

	text, err := manager.ReadResource(context.Background(),
		`raw:{"contents":[{"uri":"mem:///a","text":"BODY"}]}`)
	if err == nil {
		t.Fatalf("ReadResource returned %q with no error, want the search to fail", text)
	}
}

// TestManagerReadResourceKeepsSearchingPastAnEmptyServer is what that rule is
// for. The manager walks its servers in map order, so the answer must not depend
// on which one it happens to ask first. The repetition is what makes the
// regression visible: an implementation that takes the first empty reply as the
// answer passes a single attempt about half the time.
func TestManagerReadResourceKeepsSearchingPastAnEmptyServer(t *testing.T) {
	manager := NewManager()
	manager.Connect(context.Background(), &config.Config{MCP: config.MCP{
		Enabled: true,
		Servers: map[string]config.MCPServer{
			"empty": helperServer("no-resources"),
			"full":  helperServer("online"),
		},
	}})
	defer manager.Close()

	const uri = `raw:{"contents":[{"uri":"mem:///a","mimeType":"text/plain","text":"BODY"}]}`
	for i := 0; i < 8; i++ {
		text, err := manager.ReadResource(context.Background(), uri)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if text != "BODY" {
			t.Fatalf("attempt %d: text = %q, want %q", i, text, "BODY")
		}
	}
}
