package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The repetition nudge has to reach history after the tool results, not before.
// appendTurnMessages is pinned for that (turn_messages_test.go), but Run is
// free to ignore it: appending the nudge straight to history above executeTools
// rebuilds the invalid transcript and leaves the whole suite green, because
// ensureToolResults silently repairs the shape at send time. That silence is
// why the malformation went unnoticed in the first place.
//
// Driving Run itself would need a fake provider, so these read the call site
// instead. They are narrower than a behavioural test — a nudge written inline
// as a fresh string literal would pass — but they close the specific
// regression: reintroducing the append that used to be there fails here.

// runBody returns the parsed body of Run along with the file's position table.
func runBody(t *testing.T) (*token.FileSet, *ast.BlockStmt) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "agent.go", nil, 0)
	if err != nil {
		t.Fatalf("parse agent.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Run" && fn.Recv != nil && fn.Body != nil {
			return fset, fn.Body
		}
	}
	t.Fatal("Run is no longer a method on Agent in agent.go; this guard needs updating")
	return nil, nil
}

// appendsToHistoryBetween reports the positions of every `append(history, …)`
// falling in (from, to).
func appendsToHistoryBetween(body *ast.BlockStmt, from, to token.Pos) []token.Pos {
	var found []token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != "append" || len(call.Args) == 0 {
			return true
		}
		target, ok := call.Args[0].(*ast.Ident)
		if !ok || target.Name != "history" {
			return true
		}
		if call.Pos() > from && call.Pos() < to {
			found = append(found, call.Pos())
		}
		return true
	})
	return found
}

// callPos returns the position of the first call to the named function, which
// may be a bare name or a selector's method name.
func callPos(body *ast.BlockStmt, name string) token.Pos {
	var at token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || at.IsValid() {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == name {
				at = call.Pos()
			}
		case *ast.SelectorExpr:
			if fn.Sel.Name == name {
				at = call.Pos()
			}
		}
		return true
	})
	return at
}

// Between deciding what the repetition guard has to say and running the tools,
// nothing may go into history at all. That window is where the nudge used to
// land, and any message put there sits between an assistant's tool_calls and
// their results.
func TestRunAppendsNothingToHistoryBetweenTheRepeatCheckAndTheTools(t *testing.T) {
	fset, body := runBody(t)

	check := callPos(body, "check")
	if !check.IsValid() {
		t.Fatal("Run no longer asks the repeat tracker anything; this guard needs updating")
	}
	tools := callPos(body, "executeTools")
	if !tools.IsValid() {
		t.Fatal("Run no longer calls executeTools; this guard needs updating")
	}

	for _, at := range appendsToHistoryBetween(body, check, tools) {
		t.Errorf("%s: a message is appended to history between the repetition check and executeTools, "+
			"which puts it between the assistant's tool_calls and their results",
			fset.Position(at))
	}
}

// And the nudge itself reaches history only by being handed to
// appendTurnMessages, which puts it after the results.
func TestRunPassesTheNudgeOnlyToAppendTurnMessages(t *testing.T) {
	fset, body := runBody(t)

	const nudge = "repeatNudge"
	allowed := map[token.Pos]bool{}
	handedOver := false

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			fn, ok := node.Fun.(*ast.Ident)
			if !ok || fn.Name != "appendTurnMessages" {
				return true
			}
			for _, arg := range node.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == nudge {
					allowed[id.Pos()] = true
					handedOver = true
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == nudge {
					allowed[id.Pos()] = true
				}
			}
		case *ast.ValueSpec:
			for _, id := range node.Names {
				if id.Name == nudge {
					allowed[id.Pos()] = true
				}
			}
		}
		return true
	})

	if !handedOver {
		t.Fatalf("Run never hands %s to appendTurnMessages; either the nudge is gone or it now "+
			"reaches history another way, and this guard needs updating", nudge)
	}

	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != nudge || allowed[id.Pos()] {
			return true
		}
		t.Errorf("%s: %s is used somewhere other than its assignment and appendTurnMessages, "+
			"which is the only path that puts it after the tool results",
			fset.Position(id.Pos()), nudge)
		return true
	})
}
