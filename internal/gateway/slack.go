package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/wsutil"
)

const slackAPI = "https://slack.com/api"

// Slack connects over Socket Mode — an outbound WebSocket opened with an
// app-level token — so it needs no public URL. Replies go over the Web API with
// the bot token.
type Slack struct {
	cfg    config.Slack
	mgr    *Manager
	client *http.Client

	mu        sync.RWMutex
	connected bool
	selfID    string
}

// NewSlack builds the Slack adapter.
func NewSlack(cfg config.Slack, mgr *Manager) *Slack {
	return &Slack{cfg: cfg, mgr: mgr, client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *Slack) Name() string { return "slack" }

func (s *Slack) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

func (s *Slack) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

// Start opens the socket and reconnects until the context is cancelled.
func (s *Slack) Start(ctx context.Context) error {
	s.resolveSelf(ctx)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("slack socket dropped, reconnecting", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// run opens one socket and pumps events until it drops.
func (s *Slack) run(ctx context.Context) error {
	var open struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := s.apiCall(ctx, "apps.connections.open", s.cfg.AppToken, nil, &open); err != nil {
		return err
	}
	if !open.OK || open.URL == "" {
		return fmt.Errorf("apps.connections.open failed: %s", open.Error)
	}

	conn, err := wsutil.Dial(open.URL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(wsutil.CloseNormal, "")

	go func() {
		<-ctx.Done()
		conn.Close(wsutil.CloseNormal, "")
	}()

	for {
		_, data, err := conn.Read()
		if err != nil {
			s.setConnected(false)
			return err
		}
		var env struct {
			Type       string          `json:"type"`
			EnvelopeID string          `json:"envelope_id"`
			Payload    json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Type {
		case "hello":
			s.setConnected(true)
		case "disconnect":
			return fmt.Errorf("slack asked us to reconnect")
		case "events_api":
			// Acknowledge immediately; Slack retries unacked envelopes.
			if env.EnvelopeID != "" {
				_ = conn.WriteText(mustJSON(map[string]string{"envelope_id": env.EnvelopeID}))
			}
			s.dispatch(ctx, env.Payload)
		}
	}
}

// parseSlackMessage turns a Socket Mode payload into an inbound message,
// reporting ok=false for anything that is not a plain message from a real user
// (bots, edits, joins, our own messages).
func parseSlackMessage(payload json.RawMessage, selfID string) (InboundMessage, bool) {
	var p struct {
		Event struct {
			Type        string `json:"type"`
			Subtype     string `json:"subtype"`
			User        string `json:"user"`
			BotID       string `json:"bot_id"`
			Text        string `json:"text"`
			Channel     string `json:"channel"`
			ChannelType string `json:"channel_type"`
			TS          string `json:"ts"`
		} `json:"event"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return InboundMessage{}, false
	}
	e := p.Event
	if e.Type != "message" || e.Subtype != "" || e.BotID != "" || e.User == "" || (selfID != "" && e.User == selfID) {
		return InboundMessage{}, false
	}
	text := strings.TrimSpace(e.Text)
	if text == "" {
		return InboundMessage{}, false
	}
	return InboundMessage{
		Platform: "slack", ChannelID: e.Channel, UserID: e.User, UserName: e.User,
		Text: text, IsDirect: e.ChannelType == "im", MessageID: e.TS,
	}, true
}

// dispatch turns a Slack event into an inbound message and answers it.
func (s *Slack) dispatch(ctx context.Context, payload json.RawMessage) {
	msg, ok := parseSlackMessage(payload, s.selfIDValue())
	if !ok {
		return
	}

	if ok, denial := s.mgr.authorize(ctx, msg, s.cfg.AllowedUsers, s.cfg.AllowedChannels, s.cfg.RequirePairing); !ok {
		if denial != "" {
			_, _ = s.Send(ctx, Reply{ChannelID: msg.ChannelID, Text: denial, ReplyTo: msg.MessageID})
		}
		return
	}

	placeholder, _ := s.Send(ctx, Reply{ChannelID: msg.ChannelID, Text: "…", ReplyTo: msg.MessageID})

	var last time.Time
	final, err := s.mgr.handle(ctx, msg, func(partial string) {
		if !s.cfg.StreamEdits || placeholder == "" || partial == "" {
			return
		}
		if time.Since(last) < time.Second {
			return
		}
		last = time.Now()
		_, _ = s.Send(ctx, Reply{ChannelID: msg.ChannelID, Text: partial + " ▌", EditID: placeholder})
	})
	if err != nil {
		final = "Sorry — something went wrong: " + err.Error()
	}
	if strings.TrimSpace(final) == "" {
		final = "(no reply)"
	}
	if placeholder != "" {
		_, _ = s.Send(ctx, Reply{ChannelID: msg.ChannelID, Text: final, EditID: placeholder})
	} else {
		_, _ = s.Send(ctx, Reply{ChannelID: msg.ChannelID, Text: final, ReplyTo: msg.MessageID})
	}
}

// Send posts a new message, or edits one when EditID is set. It returns the
// message ts.
func (s *Slack) Send(ctx context.Context, r Reply) (string, error) {
	method := "chat.postMessage"
	body := map[string]any{"channel": r.ChannelID, "text": r.Text}
	if r.EditID != "" {
		method = "chat.update"
		body["ts"] = r.EditID
	} else if r.ReplyTo != "" {
		body["thread_ts"] = r.ReplyTo
	}
	var out struct {
		OK    bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error"`
	}
	if err := s.apiCall(ctx, method, s.cfg.BotToken, body, &out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("slack %s failed: %s", method, out.Error)
	}
	if out.TS != "" {
		return out.TS, nil
	}
	return r.EditID, nil
}

// resolveSelf learns the bot's own user id so it can ignore its own messages.
func (s *Slack) resolveSelf(ctx context.Context) {
	var out struct {
		OK     bool   `json:"ok"`
		UserID string `json:"user_id"`
	}
	if err := s.apiCall(ctx, "auth.test", s.cfg.BotToken, nil, &out); err == nil && out.OK {
		s.mu.Lock()
		s.selfID = out.UserID
		s.mu.Unlock()
	}
}

// apiCall posts to a Slack Web API method with the given token.
func (s *Slack) apiCall(ctx context.Context, method, token string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", slackAPI+"/"+method, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (s *Slack) selfIDValue() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selfID
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
