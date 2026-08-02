package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTempMailCreate(t *testing.T) {
	args, err := json.Marshal(map[string]any{"action": "create", "domain": "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	result := (tempMailTool{}).Execute(context.Background(), Input{Args: args})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "@example.com") {
		t.Fatalf("result = %q", result.Content)
	}
	if result.Meta["address"] == "" {
		t.Fatalf("missing address metadata: %#v", result.Meta)
	}
}

func TestTempMailValidatesActionArguments(t *testing.T) {
	tests := []map[string]any{
		{"action": "messages"},
		{"action": "wait_code", "address": "a@example.com", "timeout_seconds": 301},
		{"action": "unknown"},
	}
	for _, input := range tests {
		args, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		result := (tempMailTool{}).Execute(context.Background(), Input{Args: args})
		if !result.IsError {
			t.Errorf("input %#v succeeded: %s", input, result.Content)
		}
	}
}

func TestTempMailRegisteredInAgentToolsets(t *testing.T) {
	for _, set := range []string{"social", "default"} {
		if !containsTool(ExpandToolset(set), "temp_mail") {
			t.Errorf("toolset %q does not contain temp_mail", set)
		}
	}
	if _, ok := Default().Get("temp_mail"); !ok {
		t.Fatal("temp_mail is not registered")
	}
}

func containsTool(tools []string, want string) bool {
	for _, tool := range tools {
		if tool == want {
			return true
		}
	}
	return false
}
