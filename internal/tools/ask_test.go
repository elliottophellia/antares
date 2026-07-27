package tools

import (
	"strings"
	"testing"
)

func TestAskUserRequiresQuestion(t *testing.T) {
	res := askUserTool{}.Execute(nil, Input{Args: []byte(`{}`), Emit: func(Progress) {}})
	if !res.IsError {
		t.Fatal("a question is required")
	}
}

func TestAskUserFormatsQuestionAndOptions(t *testing.T) {
	var emitted string
	res := askUserTool{}.Execute(nil, Input{
		Args: []byte(`{"question":"Which environment?","options":["staging","prod"]}`),
		Emit: func(p Progress) { emitted = p.Message },
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Which environment?") || !strings.Contains(res.Content, "end your turn") {
		t.Fatalf("result should pose the question and yield: %s", res.Content)
	}
	if !strings.Contains(res.Content, "1. staging") || !strings.Contains(res.Content, "2. prod") {
		t.Fatalf("options not rendered: %s", res.Content)
	}
	if !strings.Contains(emitted, "staging / prod") {
		t.Fatalf("options not surfaced to the UI: %q", emitted)
	}
	if res.Meta["question"] != "Which environment?" {
		t.Fatalf("meta missing the question: %v", res.Meta)
	}
}
