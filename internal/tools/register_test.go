package tools

import (
	"encoding/json"
	"testing"
)

func TestToolsetsForUnknownToolEncodesAsEmptyArray(t *testing.T) {
	sets := ToolsetsFor("mcp__dynamic__not-in-a-preset")
	if sets == nil || len(sets) != 0 {
		t.Fatalf("ToolsetsFor unknown tool = %#v, want non-nil empty slice", sets)
	}
	got, err := json.Marshal(sets)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[]" {
		t.Fatalf("JSON = %s, want []", got)
	}
}
