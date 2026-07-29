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
	qs, ok := res.Meta["questions"].([]map[string]any)
	if !ok || len(qs) != 1 || qs[0]["question"] != "Which environment?" {
		t.Fatalf("meta should carry the normalised questions list: %v", res.Meta)
	}
}

func TestAskUserMultipleQuestions(t *testing.T) {
	res := askUserTool{}.Execute(nil, Input{
		Args: []byte(`{"questions":[
			{"question":"Pick a database","options":["sqlite","postgres"]},
			{"question":"Workspace path?"}
		]}`),
		Emit: func(Progress) {},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	// Both questions must reach the model, numbered, with the yield instruction.
	if !strings.Contains(res.Content, "1. Pick a database") || !strings.Contains(res.Content, "2. Workspace path?") {
		t.Fatalf("both questions should be posed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "end your turn") {
		t.Fatalf("result should tell the model to yield: %s", res.Content)
	}
	qs, ok := res.Meta["questions"].([]map[string]any)
	if !ok || len(qs) != 2 {
		t.Fatalf("meta should carry two questions: %v", res.Meta)
	}
}

func TestAskUserSkipsBlankQuestions(t *testing.T) {
	res := askUserTool{}.Execute(nil, Input{
		Args: []byte(`{"questions":[{"question":"  "},{"question":"Real one?"}]}`),
		Emit: func(Progress) {},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	qs, _ := res.Meta["questions"].([]map[string]any)
	if len(qs) != 1 || qs[0]["question"] != "Real one?" {
		t.Fatalf("blank question should be dropped: %v", res.Meta)
	}
}
