package agent

import (
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/llm"
)

func TestValidateToolCallArgumentsRejectsTruncatedJSON(t *testing.T) {
	resp := &llm.Response{ToolCalls: []llm.ToolCall{{Name: "browser", Arguments: `{"action":"evaluate","script":"const image =`}}}
	err := validateToolCallArguments(resp)
	if err == nil {
		t.Fatal("truncated tool arguments were accepted")
	}
	if !strings.Contains(err.Error(), "malformed tool_call arguments for browser") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !llm.Retryable(err) {
		t.Fatalf("truncated tool arguments must enter the model retry path: %v", err)
	}
}

func TestValidateToolCallArgumentsAcceptsValidJSON(t *testing.T) {
	resp := &llm.Response{ToolCalls: []llm.ToolCall{{Name: "browser", Arguments: `{"action":"snapshot"}`}}}
	if err := validateToolCallArguments(resp); err != nil {
		t.Fatalf("valid arguments rejected: %v", err)
	}
}

func TestValidateToolCallArgumentsNormalisesEmptyArguments(t *testing.T) {
	resp := &llm.Response{ToolCalls: []llm.ToolCall{{Name: "browser"}}}
	if err := validateToolCallArguments(resp); err != nil {
		t.Fatal(err)
	}
	if resp.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("arguments = %q, want {}", resp.ToolCalls[0].Arguments)
	}
}
