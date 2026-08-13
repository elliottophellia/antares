package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/tools"
)

// TestStdioRoundTrip runs this test binary as a fake MCP server (see
// TestHelperServer) and exercises the full handshake, tools/list, and
// tools/call path over stdio.
func TestStdioRoundTrip(t *testing.T) {
	client, err := Connect(context.Background(), "fake", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env:       map[string]string{"ANTARES_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	tools := client.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one tool named echo", tools)
	}
	if tools[0].Description == "" {
		t.Error("tool description was not carried through")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := client.Call(ctx, "echo", map[string]any{"text": "halo"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Text)
	}
	if res.Text != "echo: halo" {
		t.Fatalf("text = %q, want %q", res.Text, "echo: halo")
	}
}

func TestStdioReportsToolErrors(t *testing.T) {
	client, err := Connect(context.Background(), "fake", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env:       map[string]string{"ANTARES_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	res, err := client.Call(context.Background(), "boom", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected the unknown tool to be reported as an error")
	}
}

// TestStdioSelfClosesWhenChildWedged verifies that a permanently unresponsive
// child does not force every future call to wait the full timeout: after
// maxConsecutiveTimeouts back-to-back ctx timeouts the transport closes itself,
// so the next call fails fast with a connection error rather than hanging.
func TestStdioSelfClosesWhenChildWedged(t *testing.T) {
	client, err := Connect(context.Background(), "fake", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env:       map[string]string{"ANTARES_MCP_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// Drive maxConsecutiveTimeouts calls that each time out on a short context.
	for i := 0; i < maxConsecutiveTimeouts; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		_, err := client.Call(ctx, "hang", nil)
		cancel()
		if err == nil {
			t.Fatalf("call %d: expected a timeout error, got nil", i)
		}
	}

	// The transport should now be closed; a further call must fail fast rather
	// than block for its whole timeout.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, "hang", nil); err == nil {
		t.Fatal("expected the wedged transport to fail the call")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("call did not fail fast after self-close: took %s", elapsed)
	}
}

func TestEmptyToolListEncodesAsArray(t *testing.T) {
	client := &Client{}
	got := client.Tools()
	if got == nil {
		t.Fatal("Tools() returned nil, want a non-nil empty slice")
	}
	payload, err := json.Marshal(struct {
		Tools []ToolDef `json:"tools"`
	}{Tools: got})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"tools":[]}` {
		t.Fatalf("empty tool payload = %s, want tools encoded as []", payload)
	}
}

func TestRefreshReplacesToolsAndReadiness(t *testing.T) {
	cfg := helperConfig("offline")
	manager := NewManager()
	registry := tools.NewRegistry()
	manager.Connect(context.Background(), cfg)
	manager.Register(registry)
	defer manager.Close()

	status := manager.Status(cfg)
	if len(status) != 1 || !status[0].Started || status[0].Connected {
		t.Fatalf("offline status = %+v, want started but not connected", status)
	}
	if status[0].Error == "" {
		t.Fatal("offline server did not retain its tool discovery error")
	}
	if _, ok := registry.Get("mcp__fake__echo"); ok {
		t.Fatal("offline server registered a remote tool")
	}

	cfg = helperConfig("online")
	status = manager.Refresh(context.Background(), cfg)
	if len(status) != 1 || !status[0].Started || !status[0].Connected || len(status[0].Tools) != 1 {
		t.Fatalf("online status = %+v, want connected with one tool", status)
	}
	if _, ok := registry.Get("mcp__fake__echo"); !ok {
		t.Fatal("refresh did not register the newly available tool")
	}

	cfg = helperConfig("offline")
	status = manager.Refresh(context.Background(), cfg)
	if len(status) != 1 || status[0].Connected {
		t.Fatalf("second offline status = %+v, want disconnected", status)
	}
	if _, ok := registry.Get("mcp__fake__echo"); ok {
		t.Fatal("refresh left a stale remote tool registered")
	}
}

func helperConfig(mode string) *config.Config {
	return &config.Config{MCP: config.MCP{
		Enabled: true,
		Servers: map[string]config.MCPServer{
			"fake": {
				Transport: "stdio",
				Command:   os.Args[0],
				Args:      []string{"-test.run=TestHelperServer"},
				Env: map[string]string{
					"ANTARES_MCP_HELPER":      "1",
					"ANTARES_MCP_HELPER_MODE": mode,
				},
				Enabled: true,
			},
		},
	}}
}

func TestUnknownTransport(t *testing.T) {
	if _, err := Connect(context.Background(), "x", ServerConfig{Transport: "carrier-pigeon"}); err == nil {
		t.Fatal("expected an unknown transport to fail")
	}
}

func TestStdioStartupErrorIncludesChildStderr(t *testing.T) {
	_, err := Connect(context.Background(), "broken", ServerConfig{
		Transport: "stdio",
		Command:   os.Args[0],
		Args:      []string{"-test.run=TestHelperServer"},
		Env: map[string]string{
			"ANTARES_MCP_HELPER": "broken",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "MCP server exited") {
		t.Fatalf("error = %v, want child exit diagnostics", err)
	}
}

func TestExpandArgsExpandsHomeAndEnvironment(t *testing.T) {
	t.Setenv("MCP_TEST_PATH", "/tmp/mcp-test")
	got := expandArgs([]string{"${MCP_TEST_PATH}", "${HOME}/data", "~/cache"})
	if got[0] != "/tmp/mcp-test" || !strings.HasSuffix(got[1], "/data") || !strings.HasSuffix(got[2], "/cache") {
		t.Fatalf("expanded args = %#v", got)
	}
}

func TestToolNameNamespacing(t *testing.T) {
	got := mcpToolName("my server", "read/file")
	if got != "mcp__my_server__read_file" {
		t.Fatalf("mcpToolName = %q", got)
	}
}

func TestUpgradeBuiltinServersReplacesOnlyStaleCommands(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers["fetch"] = config.MCPServer{Command: "uvx", Args: []string{"mcp-server-fetch"}, Enabled: true}
	cfg.MCP.Servers["memory"] = config.MCPServer{Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"}, Enabled: true}
	cfg.MCP.Servers["linear"] = config.MCPServer{Transport: "http", URL: "https://mcp.linear.app/sse", Enabled: true}
	cfg.MCP.Servers["git"] = config.MCPServer{Command: "custom-git", Args: []string{"mcp-server-git"}, Enabled: true}

	upgradeBuiltinServers(cfg)

	fetch := cfg.MCP.Servers["fetch"]
	if len(fetch.Args) < 5 || fetch.Args[0] != "--from" || fetch.Args[3] != "mcp==1.9.4" {
		t.Fatalf("fetch args were not upgraded: %#v", fetch.Args)
	}
	if memory := cfg.MCP.Servers["memory"]; len(memory.Args) != 2 || memory.Args[1] != "@modelcontextprotocol/server-memory@2026.7.4" {
		t.Fatalf("memory args were not upgraded: %#v", memory.Args)
	}
	if linear := cfg.MCP.Servers["linear"]; linear.URL != "https://mcp.linear.app/mcp" {
		t.Fatalf("linear URL was not upgraded: %q", linear.URL)
	}
	if git := cfg.MCP.Servers["git"]; git.Command != "custom-git" || len(git.Args) != 1 {
		t.Fatalf("custom git config was changed: %+v", git)
	}
}

// TestHelperServer is not a real test: when ANTARES_MCP_HELPER is set it acts
// as a minimal MCP server speaking newline-delimited JSON-RPC on stdio.
func TestHelperServer(t *testing.T) {
	if os.Getenv("ANTARES_MCP_HELPER") == "broken" {
		os.Stderr.WriteString("synthetic startup failure\n")
		os.Exit(2)
	}
	if os.Getenv("ANTARES_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}

	reply := func(id *int64, result any) {
		out := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		b, _ := json.Marshal(out)
		os.Stdout.Write(append(b, '\n'))
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			reply(req.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake-server", "version": "0.0.1"},
			})
		case "notifications/initialized":
			// no response for notifications
		case "tools/list":
			if os.Getenv("ANTARES_MCP_HELPER_MODE") == "offline" {
				out := map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32000, "message": "backing application is offline"},
				}
				b, _ := json.Marshal(out)
				os.Stdout.Write(append(b, '\n'))
				continue
			}
			reply(req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo the supplied text back.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
					},
				}},
			})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Name == "hang" {
				// Simulate a wedged backend: never reply. The caller's context
				// timeout must return, and after enough consecutive timeouts the
				// transport self-closes.
				continue
			}
			if p.Name != "echo" {
				reply(req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{"type": "text", "text": "unknown tool " + p.Name}},
				})
				continue
			}
			text, _ := p.Arguments["text"].(string)
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + text}},
			})
		}
	}
}

var _ = exec.Command
