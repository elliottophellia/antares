package server

import (
	"errors"
	"net/http"

	"github.com/enowdev/antares/internal/board"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

func boardStore() *board.Board { return board.New(config.Path("boards")) }

// handleBoardSessions lists the sessions that have a board, for the picker.
func (s *Server) handleBoardSessions(w http.ResponseWriter, r *http.Request) {
	keys := boardStore().Keys()
	titles := map[string]string{}
	if sessions, _, err := s.db.ListSessions(r.Context(), store.SessionFilter{Limit: 500}); err == nil {
		for _, sess := range sessions {
			titles[sess.ID] = sess.Title
		}
	}
	type row struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	out := make([]row, 0, len(keys))
	for _, k := range keys {
		if k == "default" {
			continue
		}
		out = append(out, row{ID: k, Title: titles[k]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleBoard returns one session's board, grouped by column.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		writeJSON(w, http.StatusOK, map[string]any{"columns": []any{}})
		return
	}
	byCol := boardStore().List(session)
	type column struct {
		Name  string       `json:"name"`
		Cards []board.Card `json:"cards"`
	}
	order := append([]string{}, board.DefaultColumns...)
	seen := map[string]bool{}
	for _, c := range order {
		seen[c] = true
	}
	for name := range byCol {
		if !seen[name] {
			order = append(order, name)
		}
	}
	cols := make([]column, 0, len(order))
	for _, name := range order {
		cards := byCol[name]
		if cards == nil {
			cards = []board.Card{}
		}
		cols = append(cols, column{Name: name, Cards: cards})
	}
	writeJSON(w, http.StatusOK, map[string]any{"columns": cols})
}

// handleBoardRemoveCard deletes a single card from a session's board.
func (s *Server) handleBoardRemoveCard(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	id := r.URL.Query().Get("id")
	if session == "" || id == "" {
		writeError(w, http.StatusBadRequest, errors.New("session and id are required"))
		return
	}
	if _, err := boardStore().Remove(session, id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleBoardClear removes an entire session board.
func (s *Server) handleBoardClear(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		writeError(w, http.StatusBadRequest, errors.New("session is required"))
		return
	}
	if err := boardStore().Clear(session); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
