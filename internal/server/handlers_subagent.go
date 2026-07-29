package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/enowdev/antares/internal/agent"
)

// handleSwarmStream pushes the set of running sub-agents to the browser over
// SSE: the current list at once, then again whenever a sub-agent starts or
// ends. This drives the "Sub-agents" tab so it tracks the swarm live without
// polling.
func (s *Server) handleSwarmStream(w http.ResponseWriter, r *http.Request) {
	// Scope to the delegating session so a chat only sees the sub-agents it
	// spawned; without this, workers from another session leak into this list.
	session := r.URL.Query().Get("session")
	sse, err := newSSE(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ch, cancel := agent.SubscribeSwarm()
	defer cancel()

	send := func() error {
		return sse.send(map[string]any{"active": s.agent.ActiveAgentsFor(session)})
	}
	if err := send(); err != nil {
		return
	}

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			sse.comment("keepalive")
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := send(); err != nil {
				return
			}
		}
	}
}

// handleSubAgentAttach follows one sub-agent's live event stream, replaying from
// the given cursor and then following until the sub-agent finishes or the client
// disconnects. If the sub-agent is not (or no longer) running, it reports done at
// once so the UI falls back to the main transcript. The event shape is identical
// to the main chat stream, so the client renders it with the same code.
func (s *Server) handleSubAgentAttach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))

	sse, err := newSSE(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	stream := agent.GetSubStream(id)
	if stream == nil {
		_ = sse.send(agent.Event{Type: agent.EventDone})
		return
	}

	ctx := r.Context()
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				sse.comment("keepalive")
			}
		}
	}()

	_ = stream.Follow(ctx, cursor, func(e agent.Event) error { return sse.send(e) })
}
