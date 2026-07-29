package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/gateway"
)

// handleDiscordGuilds lists the servers the Discord bot belongs to, so the
// binding UI can offer a picker instead of asking for raw snowflake ids.
func (s *Server) handleDiscordGuilds(w http.ResponseWriter, r *http.Request) {
	token := s.config().Gateway.Discord.BotToken
	guilds, err := gateway.ListDiscordGuilds(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"guilds": []any{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guilds": guilds})
}

// handleDiscordChannels lists a guild's text channels for the binding UI.
func (s *Server) handleDiscordChannels(w http.ResponseWriter, r *http.Request) {
	token := s.config().Gateway.Discord.BotToken
	channels, err := gateway.ListDiscordChannels(r.Context(), token, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []any{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

// handleListBindings returns the configured routing bindings.
func (s *Server) handleListBindings(w http.ResponseWriter, r *http.Request) {
	bindings := s.config().Gateway.Bindings
	if bindings == nil {
		bindings = []config.Binding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
}

// handleSaveBinding creates or updates one routing binding. A binding with an
// existing id is replaced; one without gets a fresh id. The gateway needs no
// reconnect — routing is resolved per message from the live config.
func (s *Server) handleSaveBinding(w http.ResponseWriter, r *http.Request) {
	var b config.Binding
	if err := decodeBody(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	b.Platform = strings.ToLower(strings.TrimSpace(b.Platform))
	if b.Platform != "discord" && b.Platform != "telegram" {
		writeError(w, http.StatusBadRequest, errors.New("platform must be discord or telegram"))
		return
	}
	if b.Platform == "telegram" && strings.TrimSpace(b.ChannelID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("a Telegram binding needs a chat id"))
		return
	}
	if b.Platform == "discord" && strings.TrimSpace(b.GuildID) == "" && strings.TrimSpace(b.ChannelID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("a Discord binding needs a server or channel"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Replace by id, or append with a new id.
	if strings.TrimSpace(b.ID) != "" {
		found := false
		for i := range cfg.Gateway.Bindings {
			if cfg.Gateway.Bindings[i].ID == b.ID {
				cfg.Gateway.Bindings[i] = b
				found = true
				break
			}
		}
		if !found {
			cfg.Gateway.Bindings = append(cfg.Gateway.Bindings, b)
		}
	} else {
		b.ID = newID("bind")
		cfg.Gateway.Bindings = append(cfg.Gateway.Bindings, b)
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "binding": b})
}

// handleDeleteBinding removes one routing binding by id.
func (s *Server) handleDeleteBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := cfg.Gateway.Bindings[:0]
	removed := false
	for _, b := range cfg.Gateway.Bindings {
		if b.ID == id {
			removed = true
			continue
		}
		out = append(out, b)
	}
	if !removed {
		writeError(w, http.StatusNotFound, errors.New("no binding by that id"))
		return
	}
	cfg.Gateway.Bindings = out
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
