package board

import "testing"

func TestBoardAddMoveList(t *testing.T) {
	b := New(t.TempDir())
	c, err := b.Add("s1", "write tests", "", "")
	if err != nil || c.Column != "todo" {
		t.Fatalf("add failed: %+v %v", c, err)
	}
	// default column normalises to todo.
	if _, ok, _ := b.Move("s1", c.ID, "doing"); !ok {
		t.Fatal("move should find the card")
	}
	cols := b.List("s1")
	if len(cols["doing"]) != 1 || len(cols["todo"]) != 0 {
		t.Fatalf("card not moved: %+v", cols)
	}
}

func TestBoardColumnNormalisation(t *testing.T) {
	b := New(t.TempDir())
	c, _ := b.Add("s1", "x", "", "in-progress")
	if c.Column != "doing" {
		t.Fatalf("in-progress should normalise to doing, got %s", c.Column)
	}
}

func TestBoardRemoveAndIsolation(t *testing.T) {
	b := New(t.TempDir())
	c, _ := b.Add("s1", "x", "", "todo")
	_, _ = b.Add("s2", "other", "", "todo")
	ok, _ := b.Remove("s1", c.ID)
	if !ok || len(b.List("s1")["todo"]) != 0 {
		t.Fatal("remove failed")
	}
	// s2 is a separate board.
	if len(b.List("s2")["todo"]) != 1 {
		t.Fatal("boards should be per-key")
	}
	if ok, _ := b.Remove("s1", "nope"); ok {
		t.Fatal("removing an unknown card should report false")
	}
}

func TestBoardRejectsEmptyTitle(t *testing.T) {
	b := New(t.TempDir())
	if _, err := b.Add("s1", "", "", ""); err == nil {
		t.Fatal("a card needs a title")
	}
}
