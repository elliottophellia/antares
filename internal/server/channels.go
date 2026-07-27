package server

import (
	"errors"
	"net/http"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/gateway"
)

// channelField describes one credential input a channel needs.
type channelField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret"`
	Placeholder string `json:"placeholder,omitempty"`
	Set         bool   `json:"set"` // already has a value (secrets are never echoed)
}

// channelSpec is the static shape of a channel: its label, help, and fields.
type channelSpec struct {
	ID     string
	Label  string
	Detail string
	Docs   string
	Fields []channelField
}

// channelSpecs is the catalogue of supported gateways.
func channelSpecs() []channelSpec {
	return []channelSpec{
		{ID: "telegram", Label: "Telegram", Docs: "https://t.me/BotFather",
			Detail: "Telegram bot — long polling, no public domain required.",
			Fields: []channelField{{Key: "bot_token", Label: "Bot token", Secret: true}}},
		{ID: "discord", Label: "Discord", Docs: "https://discord.com/developers/applications",
			Detail: "Discord bot — websocket gateway.",
			Fields: []channelField{{Key: "bot_token", Label: "Bot token", Secret: true}}},
		{ID: "slack", Label: "Slack", Docs: "https://api.slack.com/apps",
			Detail: "Slack Socket Mode — app-level + bot token, no public URL.",
			Fields: []channelField{
				{Key: "app_token", Label: "App-level token (xapp-…)", Secret: true},
				{Key: "bot_token", Label: "Bot token (xoxb-…)", Secret: true},
			}},
		{ID: "matrix", Label: "Matrix", Docs: "https://matrix.org",
			Detail: "Matrix bot — /sync long-poll against a homeserver.",
			Fields: []channelField{
				{Key: "homeserver", Label: "Homeserver URL", Placeholder: "https://matrix.org"},
				{Key: "user_id", Label: "Bot user id", Placeholder: "@bot:matrix.org"},
				{Key: "access_token", Label: "Access token", Secret: true},
			}},
		{ID: "signal", Label: "Signal", Docs: "https://github.com/bbernhard/signal-cli-rest-api",
			Detail: "Signal — polls a signal-cli REST daemon you run.",
			Fields: []channelField{
				{Key: "api_url", Label: "signal-cli API URL", Placeholder: "http://localhost:8080"},
				{Key: "number", Label: "Bot number", Placeholder: "+15551234567"},
			}},
		{ID: "whatsapp", Label: "WhatsApp", Docs: "https://developers.facebook.com/docs/whatsapp/cloud-api",
			Detail: "WhatsApp Cloud API — a webhook listener plus the Graph API.",
			Fields: []channelField{
				{Key: "token", Label: "Graph API token", Secret: true},
				{Key: "phone_number_id", Label: "Phone number id"},
				{Key: "verify_token", Label: "Webhook verify token", Secret: true},
				{Key: "listen_addr", Label: "Listen address", Placeholder: ":8090"},
				{Key: "path", Label: "Webhook path", Placeholder: "/webhook"},
			}},
		{ID: "feishu", Label: "Feishu / Lark", Docs: "https://open.feishu.cn",
			Detail: "Feishu — an event webhook plus a tenant token.",
			Fields: []channelField{
				{Key: "app_id", Label: "App id"},
				{Key: "app_secret", Label: "App secret", Secret: true},
				{Key: "verify_token", Label: "Verify token", Secret: true},
				{Key: "listen_addr", Label: "Listen address", Placeholder: ":8091"},
				{Key: "path", Label: "Webhook path", Placeholder: "/webhook"},
			}},
	}
}

// channelValue reads one channel field from the config.
func channelValue(cfg *config.Config, id, key string) string {
	g := &cfg.Gateway
	switch id {
	case "telegram":
		if key == "bot_token" {
			return g.Telegram.BotToken
		}
	case "discord":
		if key == "bot_token" {
			return g.Discord.BotToken
		}
	case "slack":
		switch key {
		case "app_token":
			return g.Slack.AppToken
		case "bot_token":
			return g.Slack.BotToken
		}
	case "matrix":
		switch key {
		case "homeserver":
			return g.Matrix.Homeserver
		case "user_id":
			return g.Matrix.UserID
		case "access_token":
			return g.Matrix.AccessToken
		}
	case "signal":
		switch key {
		case "api_url":
			return g.Signal.APIURL
		case "number":
			return g.Signal.Number
		}
	case "whatsapp":
		switch key {
		case "token":
			return g.WhatsApp.Token
		case "phone_number_id":
			return g.WhatsApp.PhoneNumberID
		case "verify_token":
			return g.WhatsApp.VerifyToken
		case "listen_addr":
			return g.WhatsApp.ListenAddr
		case "path":
			return g.WhatsApp.Path
		}
	case "feishu":
		switch key {
		case "app_id":
			return g.Feishu.AppID
		case "app_secret":
			return g.Feishu.AppSecret
		case "verify_token":
			return g.Feishu.VerifyToken
		case "listen_addr":
			return g.Feishu.ListenAddr
		case "path":
			return g.Feishu.Path
		}
	}
	return ""
}

// setChannelValue writes one channel field into the config.
func setChannelValue(cfg *config.Config, id, key, val string) {
	g := &cfg.Gateway
	switch id {
	case "telegram":
		if key == "bot_token" {
			g.Telegram.BotToken = val
		}
	case "discord":
		if key == "bot_token" {
			g.Discord.BotToken = val
		}
	case "slack":
		switch key {
		case "app_token":
			g.Slack.AppToken = val
		case "bot_token":
			g.Slack.BotToken = val
		}
	case "matrix":
		switch key {
		case "homeserver":
			g.Matrix.Homeserver = val
		case "user_id":
			g.Matrix.UserID = val
		case "access_token":
			g.Matrix.AccessToken = val
		}
	case "signal":
		switch key {
		case "api_url":
			g.Signal.APIURL = val
		case "number":
			g.Signal.Number = val
		}
	case "whatsapp":
		switch key {
		case "token":
			g.WhatsApp.Token = val
		case "phone_number_id":
			g.WhatsApp.PhoneNumberID = val
		case "verify_token":
			g.WhatsApp.VerifyToken = val
		case "listen_addr":
			g.WhatsApp.ListenAddr = val
		case "path":
			g.WhatsApp.Path = val
		}
	case "feishu":
		switch key {
		case "app_id":
			g.Feishu.AppID = val
		case "app_secret":
			g.Feishu.AppSecret = val
		case "verify_token":
			g.Feishu.VerifyToken = val
		case "listen_addr":
			g.Feishu.ListenAddr = val
		case "path":
			g.Feishu.Path = val
		}
	}
}

// channelEnabled / setChannelEnabled read and write a channel's enable flag.
func channelEnabled(cfg *config.Config, id string) bool {
	switch id {
	case "telegram":
		return cfg.Gateway.Telegram.Enabled
	case "discord":
		return cfg.Gateway.Discord.Enabled
	case "slack":
		return cfg.Gateway.Slack.Enabled
	case "matrix":
		return cfg.Gateway.Matrix.Enabled
	case "signal":
		return cfg.Gateway.Signal.Enabled
	case "whatsapp":
		return cfg.Gateway.WhatsApp.Enabled
	case "feishu":
		return cfg.Gateway.Feishu.Enabled
	}
	return false
}

func setChannelEnabled(cfg *config.Config, id string, v bool) {
	switch id {
	case "telegram":
		cfg.Gateway.Telegram.Enabled = v
	case "discord":
		cfg.Gateway.Discord.Enabled = v
	case "slack":
		cfg.Gateway.Slack.Enabled = v
	case "matrix":
		cfg.Gateway.Matrix.Enabled = v
	case "signal":
		cfg.Gateway.Signal.Enabled = v
	case "whatsapp":
		cfg.Gateway.WhatsApp.Enabled = v
	case "feishu":
		cfg.Gateway.Feishu.Enabled = v
	}
}

// channelConfigured reports whether the channel has the fields it needs to run.
// The required set is every non-optional field (listen_addr/path have defaults).
func channelConfigured(cfg *config.Config, spec channelSpec) bool {
	for _, f := range spec.Fields {
		if f.Key == "listen_addr" || f.Key == "path" || f.Key == "user_id" {
			continue // optional / has a default
		}
		if channelValue(cfg, spec.ID, f.Key) == "" {
			return false
		}
	}
	return true
}

func channelSpecByID(id string) (channelSpec, bool) {
	for _, s := range channelSpecs() {
		if s.ID == id {
			return s, true
		}
	}
	return channelSpec{}, false
}

// handleSetChannelConfig writes a channel's credential fields, then reconnects.
func (s *Server) handleSetChannelConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fields  map[string]string `json:"fields"`
		Enabled *bool             `json:"enabled"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	spec, ok := channelSpecByID(id)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("unknown channel"))
		return
	}

	cfg, err := config.Reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Telegram and Discord verify their token with the platform, so a bad paste
	// fails here and the bot's name comes back as confirmation.
	var identity any
	if (id == "telegram" || id == "discord") && body.Fields["bot_token"] != "" {
		who, verr := gateway.VerifyToken(r.Context(), id, body.Fields["bot_token"])
		if verr != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": verr.Error()})
			return
		}
		identity = who
	}

	// Only overwrite a field the form actually sent — an empty secret input
	// means "leave it", not "clear it".
	for _, f := range spec.Fields {
		if v, present := body.Fields[f.Key]; present && v != "" {
			setChannelValue(cfg, id, f.Key, v)
		}
	}
	if body.Enabled != nil {
		if *body.Enabled && !channelConfigured(cfg, spec) {
			writeError(w, http.StatusBadRequest, errors.New("fill in the required fields before enabling"))
			return
		}
		setChannelEnabled(cfg, id, *body.Enabled)
		if *body.Enabled {
			cfg.Gateway.Enabled = true
		}
	}

	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.applyReload(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := map[string]any{"ok": true}
	if identity != nil {
		out["bot"] = identity
	}
	if s.gateway != nil {
		if err := s.gateway.Sync(id); err != nil {
			out["restart_required"] = true
		}
	}
	writeJSON(w, http.StatusOK, out)
}
