// Package board is a simple Kanban board: cards in columns, persisted to disk.
// It is a richer cousin of the todo list — the agent can lay work out visually
// and move it across "todo", "doing", and "done" as it goes.
package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultColumns are the stages a card moves through.
var DefaultColumns = []string{"todo", "doing", "done"}

// Card is one item on the board.
type Card struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Note      string    `json:"note,omitempty"`
	Column    string    `json:"column"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Board persists cards for one key (a session) as a single JSON file.
type Board struct {
	dir string
	mu  sync.Mutex
}

// New opens a board store under dir.
func New(dir string) *Board { return &Board{dir: dir} }

func (b *Board) path(key string) string { return filepath.Join(b.dir, safeName(key)+".json") }

// Add creates a card in a column (default "todo").
func (b *Board) Add(key, title, note, column string) (Card, error) {
	if strings.TrimSpace(title) == "" {
		return Card{}, fmt.Errorf("a card needs a title")
	}
	column = normColumn(column)
	b.mu.Lock()
	defer b.mu.Unlock()
	cards := b.load(key)
	now := time.Now()
	c := Card{
		ID: fmt.Sprintf("c%d", len(cards)+1), Title: title, Note: note, Column: column,
		CreatedAt: now, UpdatedAt: now,
	}
	// Give it a stable, unique id even after removals.
	c.ID = fmt.Sprintf("c%d", now.UnixNano()%1000000)
	cards = append(cards, c)
	return c, b.save(key, cards)
}

// Move sends a card to another column.
func (b *Board) Move(key, id, column string) (Card, bool, error) {
	column = normColumn(column)
	b.mu.Lock()
	defer b.mu.Unlock()
	cards := b.load(key)
	for i := range cards {
		if cards[i].ID == id {
			cards[i].Column = column
			cards[i].UpdatedAt = time.Now()
			return cards[i], true, b.save(key, cards)
		}
	}
	return Card{}, false, nil
}

// Remove deletes a card.
func (b *Board) Remove(key, id string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cards := b.load(key)
	out := cards[:0]
	found := false
	for _, c := range cards {
		if c.ID == id {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return false, nil
	}
	return true, b.save(key, out)
}

// Clear removes an entire board (all cards for a key) by deleting its file.
func (b *Board) Clear(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.Remove(b.path(key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns the cards grouped by column, in column order.
func (b *Board) List(key string) map[string][]Card {
	b.mu.Lock()
	defer b.mu.Unlock()
	cards := b.load(key)
	byCol := map[string][]Card{}
	for _, c := range cards {
		byCol[c.Column] = append(byCol[c.Column], c)
	}
	for col := range byCol {
		sort.Slice(byCol[col], func(i, j int) bool { return byCol[col][i].CreatedAt.Before(byCol[col][j].CreatedAt) })
	}
	return byCol
}

// Keys lists the session keys that have a board.
func (b *Board) Keys() []string {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return out
}

func normColumn(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	switch c {
	case "", "todo", "backlog", "to-do":
		return "todo"
	case "doing", "in-progress", "wip", "in progress":
		return "doing"
	case "done", "complete", "completed":
		return "done"
	default:
		return c // allow custom columns
	}
}

func (b *Board) load(key string) []Card {
	raw, err := os.ReadFile(b.path(key))
	if err != nil {
		return nil
	}
	var cards []Card
	if json.Unmarshal(raw, &cards) != nil {
		return nil
	}
	return cards
}

func (b *Board) save(key string, cards []Card) error {
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cards, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path(key) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, b.path(key))
}

func safeName(s string) string {
	if s == "" {
		return "default"
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
	return out
}
