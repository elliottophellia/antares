package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexBuildsResponsesInput(t *testing.T) {
	c := &codexClient{opts: Options{BaseURL: "https://api.openai.com/v1"}}
	req := Request{
		Model:  "gpt-5-codex",
		System: "be terse",
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Arguments: `{"p":"x"}`}}},
			{Role: RoleTool, ToolCallID: "call_1", Content: "file body"},
		},
	}
	body := c.bodyJSON(req)
	if !strings.Contains(body, `"instructions":"be terse"`) {
		t.Fatalf("system should map to instructions: %s", body)
	}
	if !strings.Contains(body, `"function_call_output"`) || !strings.Contains(body, `"call_1"`) {
		t.Fatalf("tool result not mapped: %s", body)
	}
	if !strings.Contains(body, `"input_text"`) {
		t.Fatalf("user text not mapped: %s", body)
	}
}

func TestCodexParsesResponse(t *testing.T) {
	raw := `{"output":[
	  {"type":"reasoning","summary":[{"text":"thinking"}]},
	  {"type":"message","role":"assistant","content":[{"type":"output_text","text":"the answer"}]},
	  {"type":"function_call","call_id":"c1","name":"run","arguments":"{}"}
	],"usage":{"input_tokens":10,"output_tokens":5}}`
	var r responsesReply
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	resp, err := r.toResponse()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "the answer" || resp.Reasoning != "thinking" {
		t.Fatalf("bad parse: %+v", resp)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "run" {
		t.Fatalf("tool call not parsed: %+v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 10 {
		t.Fatalf("usage not parsed: %+v", resp.Usage)
	}
}

func TestNewCodexConstructs(t *testing.T) {
	c, err := New(Options{Kind: "codex", APIKey: "sk-x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*codexClient); !ok {
		t.Fatalf("expected a codex client, got %T", c)
	}
}
