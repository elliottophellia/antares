package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/board"
)

// boardTool is a Kanban board: cards laid out across columns the agent moves
// them through. It is a visual companion to the todo list for longer work.
type boardTool struct{}

func (boardTool) Name() string { return "board" }
func (boardTool) Description() string {
	return "A Kanban board for laying out work visually across columns (todo, doing, done). " +
		"`add` puts a card in a column, `move` sends a card to another column, `list` shows the board, " +
		"and `remove` deletes a card. Use it for multi-part work you want to track by stage."
}
func (boardTool) Schema() map[string]any {
	return schema(map[string]any{
		"action": propEnum("What to do.", "list", "add", "move", "remove"),
		"title":  prop("string", "For add: the card title."),
		"note":   prop("string", "For add: optional detail."),
		"column": prop("string", "Column: todo, doing, or done (or a custom name)."),
		"id":     prop("string", "For move/remove: the card id."),
	}, "action")
}

func (boardTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Action string `json:"action"`
		Title  string `json:"title"`
		Note   string `json:"note"`
		Column string `json:"column"`
		ID     string `json:"id"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Board == nil {
		return Errorf("the board is not available in this runtime")
	}
	b := in.Deps.Board
	key := in.SessionID

	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "list", "":
		return Text(renderBoard(b.List(key)))
	case "add", "new":
		c, err := b.Add(key, args.Title, args.Note, args.Column)
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(fmt.Sprintf("Added %s to %s: %s", c.ID, c.Column, c.Title))
	case "move":
		if args.ID == "" || args.Column == "" {
			return Errorf("id and column are required to move a card")
		}
		c, ok, err := b.Move(key, args.ID, args.Column)
		if err != nil {
			return Errorf("%v", err)
		}
		if !ok {
			return Errorf("no card %q — use action=list to see the ids", args.ID)
		}
		return Text(fmt.Sprintf("Moved %s to %s.", c.ID, c.Column))
	case "remove", "delete":
		if args.ID == "" {
			return Errorf("id is required to remove a card")
		}
		ok, err := b.Remove(key, args.ID)
		if err != nil {
			return Errorf("%v", err)
		}
		if !ok {
			return Errorf("no card %q", args.ID)
		}
		return Text("Removed " + args.ID + ".")
	default:
		return Errorf("unknown action %q (want list, add, move, or remove)", args.Action)
	}
}

func renderBoard(byCol map[string][]board.Card) string {
	if len(byCol) == 0 {
		return "The board is empty. Add a card with action=add."
	}
	var b strings.Builder
	// Show the standard columns in order, then any custom ones.
	shown := map[string]bool{}
	for _, col := range board.DefaultColumns {
		writeColumn(&b, col, byCol[col])
		shown[col] = true
	}
	for col, cards := range byCol {
		if !shown[col] {
			writeColumn(&b, col, cards)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeColumn(b *strings.Builder, col string, cards []board.Card) {
	fmt.Fprintf(b, "## %s (%d)\n", col, len(cards))
	for _, c := range cards {
		fmt.Fprintf(b, "- %s %s", c.ID, c.Title)
		if c.Note != "" {
			fmt.Fprintf(b, " — %s", c.Note)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}
