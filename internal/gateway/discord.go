package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/version"
	"github.com/enowdev/antares/internal/wsutil"
)

const discordAPI = "https://discord.com/api/v10"

// Gateway opcodes.
const (
	dcDispatch       = 0
	dcHeartbeat      = 1
	dcIdentify       = 2
	dcResume         = 6
	dcReconnect      = 7
	dcInvalidSession = 9
	dcHello          = 10
	dcHeartbeatACK   = 11
)

// DefaultIntents covers guild messages, message content, and direct messages.
const DefaultIntents = (1 << 9) | (1 << 15) | (1 << 12)

// Discord connects to the bot gateway over WebSocket and replies over REST.
type Discord struct {
	cfg    config.Discord
	mgr    *Manager
	client *http.Client

	mu        sync.RWMutex
	connected bool
	selfID    string
	sessionID string
	resumeURL string
	seq       int64
}

// NewDiscord builds the Discord adapter.
func NewDiscord(cfg config.Discord, mgr *Manager) *Discord {
	if cfg.Intents == 0 {
		cfg.Intents = DefaultIntents
	}
	return &Discord{cfg: cfg, mgr: mgr, client: &http.Client{Timeout: 30 * time.Second}}
}

func (d *Discord) Name() string { return "discord" }

func (d *Discord) Connected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.connected
}

func (d *Discord) setConnected(v bool) {
	d.mu.Lock()
	d.connected = v
	d.mu.Unlock()
}

// rest performs one REST call against the Discord API.
func (d *Discord) rest(ctx context.Context, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, discordAPI+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+d.cfg.BotToken)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/enowdev/antares, "+version.Version+")")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		var limit struct {
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.Unmarshal(data, &limit)
		wait := time.Duration(limit.RetryAfter * float64(time.Second))
		if wait <= 0 || wait > time.Minute {
			wait = 2 * time.Second
		}
		slog.Warn("discord rate limited", "retry_after", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		return d.rest(ctx, method, path, payload, out)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// gatewayPayload is the envelope for every gateway frame.
type gatewayPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

type dcAuthor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

type dcMessage struct {
	ID        string     `json:"id"`
	ChannelID string     `json:"channel_id"`
	GuildID   string     `json:"guild_id"`
	Content   string     `json:"content"`
	Author    dcAuthor   `json:"author"`
	Mentions  []dcAuthor `json:"mentions"`
}

// Start connects to the gateway and processes events until ctx is cancelled.
func (d *Discord) Start(ctx context.Context) error {
	var info struct {
		URL string `json:"url"`
	}
	if err := d.rest(ctx, "GET", "/gateway/bot", nil, &info); err != nil {
		return fmt.Errorf("gateway discovery: %w", err)
	}

	d.mu.RLock()
	resumeURL, sessionID := d.resumeURL, d.sessionID
	d.mu.RUnlock()

	wsURL := info.URL
	resuming := sessionID != "" && resumeURL != ""
	if resuming {
		wsURL = resumeURL
	}
	if !strings.Contains(wsURL, "?") {
		wsURL += "?v=10&encoding=json"
	}

	conn, err := wsutil.Dial(wsURL, &wsutil.DialOptions{Timeout: 30 * time.Second})
	if err != nil {
		return fmt.Errorf("gateway dial: %w", err)
	}
	defer conn.Close(wsutil.CloseNormal, "")

	// HELLO must arrive first and carries the heartbeat interval.
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, raw, err := conn.Read()
	if err != nil {
		return fmt.Errorf("waiting for HELLO: %w", err)
	}
	var hello gatewayPayload
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Op != dcHello {
		return fmt.Errorf("expected HELLO, got op %d", hello.Op)
	}
	var helloData struct {
		HeartbeatInterval int64 `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &helloData); err != nil {
		return fmt.Errorf("decode HELLO: %w", err)
	}
	interval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	if interval <= 0 {
		interval = 41250 * time.Millisecond
	}

	send := func(op int, data any) error {
		payload := map[string]any{"op": op}
		if data != nil {
			payload["d"] = data
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return conn.WriteText(b)
	}

	if resuming {
		d.mu.RLock()
		seq := d.seq
		d.mu.RUnlock()
		err = send(dcResume, map[string]any{
			"token": d.cfg.BotToken, "session_id": sessionID, "seq": seq,
		})
	} else {
		err = send(dcIdentify, map[string]any{
			"token":   d.cfg.BotToken,
			"intents": d.cfg.Intents,
			"properties": map[string]string{
				"os": "linux", "browser": "antares", "device": "antares",
			},
		})
	}
	if err != nil {
		return fmt.Errorf("identify: %w", err)
	}

	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()

	go func() {
		// Discord asks clients to jitter the first heartbeat.
		first := time.Duration(rand.Float64() * float64(interval))
		timer := time.NewTimer(first)
		defer timer.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-timer.C:
				d.mu.RLock()
				seq := d.seq
				d.mu.RUnlock()
				var data any
				if seq > 0 {
					data = seq
				}
				if err := send(dcHeartbeat, data); err != nil {
					slog.Debug("discord heartbeat failed", "error", err)
					return
				}
				timer.Reset(interval)
			}
		}
	}()

	for {
		if ctx.Err() != nil {
			return nil
		}
		// Allow ample slack beyond the heartbeat interval before declaring the
		// connection dead.
		_ = conn.SetReadDeadline(time.Now().Add(interval * 2))
		_, raw, err := conn.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			d.setConnected(false)
			return fmt.Errorf("gateway read: %w", err)
		}

		var ev gatewayPayload
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		if ev.S != nil {
			d.mu.Lock()
			d.seq = *ev.S
			d.mu.Unlock()
		}

		switch ev.Op {
		case dcHeartbeat:
			d.mu.RLock()
			seq := d.seq
			d.mu.RUnlock()
			_ = send(dcHeartbeat, seq)

		case dcHeartbeatACK:
			// nothing to do

		case dcReconnect:
			slog.Info("discord asked us to reconnect")
			return nil

		case dcInvalidSession:
			slog.Warn("discord session invalidated; re-identifying")
			d.mu.Lock()
			d.sessionID, d.resumeURL, d.seq = "", "", 0
			d.mu.Unlock()
			return nil

		case dcDispatch:
			d.dispatch(ctx, ev)
		}
	}
}

// dispatch routes one gateway event.
func (d *Discord) dispatch(ctx context.Context, ev gatewayPayload) {
	switch ev.T {
	case "READY":
		var ready struct {
			SessionID        string   `json:"session_id"`
			ResumeGatewayURL string   `json:"resume_gateway_url"`
			User             dcAuthor `json:"user"`
		}
		if err := json.Unmarshal(ev.D, &ready); err != nil {
			return
		}
		d.mu.Lock()
		d.sessionID, d.resumeURL, d.selfID = ready.SessionID, ready.ResumeGatewayURL, ready.User.ID
		appID := d.selfID
		token := d.cfg.BotToken
		d.mu.Unlock()
		d.setConnected(true)
		slog.Info("discord connected", "bot", ready.User.Username)

		// Publish slash commands so they appear natively. Off the dispatch path
		// so a slow or failed registration never stalls the connection.
		go func() {
			if err := RegisterDiscordCommands(context.Background(), token, appID); err != nil {
				slog.Warn("discord: could not register slash commands", "error", err)
			} else {
				slog.Info("discord slash commands registered")
			}
		}()

	case "RESUMED":
		d.setConnected(true)
		slog.Info("discord session resumed")

	case "MESSAGE_CREATE":
		var m dcMessage
		if err := json.Unmarshal(ev.D, &m); err != nil {
			return
		}
		go d.handleMessage(ctx, m)

	case "INTERACTION_CREATE":
		var it dcInteraction
		if err := json.Unmarshal(ev.D, &it); err != nil {
			return
		}
		go d.handleInteraction(ctx, it)
	}
}

// dcInteraction is a slash-command invocation. Type 2 is APPLICATION_COMMAND.
type dcInteraction struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Type  int    `json:"type"`
	// GuildID/ChannelID locate the invocation; Member.User (guild) or User (DM)
	// identifies the caller.
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Member    *struct {
		User dcAuthor `json:"user"`
	} `json:"member"`
	User *dcAuthor `json:"user"`
	Data struct {
		Name    string `json:"name"`
		Options []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"options"`
	} `json:"data"`
}

// handleInteraction answers a slash command. It ACKs immediately (Discord
// requires a response within 3s), runs the command as a synthetic "/name args"
// message through the same path typed commands take, then edits the ACK with
// the result.
func (d *Discord) handleInteraction(ctx context.Context, it dcInteraction) {
	if it.Type != 2 {
		return
	}
	// Defer the reply (type 5) so Discord shows "thinking…" while the agent works.
	_ = d.rest(ctx, "POST", "/interactions/"+it.ID+"/"+it.Token+"/callback",
		map[string]any{"type": 5}, nil)

	author := it.User
	if author == nil && it.Member != nil {
		author = &it.Member.User
	}
	if author == nil {
		author = &dcAuthor{}
	}

	// Rebuild the command line from the interaction options.
	line := "/" + it.Data.Name
	for _, o := range it.Data.Options {
		line += " " + fmt.Sprintf("%v", o.Value)
	}

	msg := InboundMessage{
		Platform: "discord", ChannelID: it.ChannelID, GuildID: it.GuildID,
		UserID: author.ID, UserName: author.Username, Text: line,
		IsDirect: it.GuildID == "", MessageID: it.ID,
	}

	reply, err := d.mgr.handle(ctx, msg, nil)
	if err != nil {
		reply = "⚠️ " + err.Error()
	}
	if strings.TrimSpace(reply) == "" {
		reply = "(no reply)"
	}

	// Edit the deferred reply with the first chunk; follow up with the rest.
	chunks := splitForDiscord(reply)
	if len(chunks) == 0 {
		chunks = []string{"(no reply)"}
	}
	_ = d.rest(ctx, "PATCH", "/webhooks/"+d.appID()+"/"+it.Token+"/messages/@original",
		map[string]any{"content": chunks[0]}, nil)
	for _, chunk := range chunks[1:] {
		_ = d.rest(ctx, "POST", "/webhooks/"+d.appID()+"/"+it.Token,
			map[string]any{"content": chunk}, nil)
	}
}

// appID returns the bot's application id (its own user id).
func (d *Discord) appID() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.selfID
}

// handleMessage authorises and answers one Discord message.
func (d *Discord) handleMessage(ctx context.Context, m dcMessage) {
	d.mu.RLock()
	selfID := d.selfID
	d.mu.RUnlock()

	if m.Author.Bot || m.Author.ID == selfID {
		return
	}
	text := strings.TrimSpace(m.Content)
	if text == "" {
		// An empty content on a real user message almost always means the
		// Message Content Intent is off in the Developer Portal.
		slog.Warn("discord: received a message with empty content — enable Message Content Intent in the Developer Portal",
			"channel", m.ChannelID, "guild", m.GuildID)
		return
	}

	isDirect := m.GuildID == ""
	if !isDirect {
		// In a guild the bot only answers when mentioned.
		mentioned := false
		for _, u := range m.Mentions {
			if u.ID == selfID {
				mentioned = true
				break
			}
		}
		if !mentioned {
			slog.Debug("discord: ignoring un-addressed guild message (mention the bot to talk in a server)",
				"channel", m.ChannelID)
			return
		}
		text = strings.TrimSpace(strings.NewReplacer(
			"<@"+selfID+">", "", "<@!"+selfID+">", "",
		).Replace(text))
	}

	if len(d.cfg.AllowedGuilds) > 0 && m.GuildID != "" && !contains(d.cfg.AllowedGuilds, m.GuildID) {
		return
	}

	msg := InboundMessage{
		Platform: "discord", ChannelID: m.ChannelID, GuildID: m.GuildID, UserID: m.Author.ID,
		UserName: m.Author.Username, Text: text, IsDirect: isDirect, MessageID: m.ID,
	}

	allowed, denial := d.mgr.authorize(ctx, msg, d.cfg.AllowedUsers, nil, d.cfg.RequirePairing)
	if !allowed {
		if denial != "" {
			_, _ = d.Send(ctx, Reply{ChannelID: m.ChannelID, Text: denial, ReplyTo: m.ID})
		}
		return
	}

	// The typing indicator lasts ~10s, so refresh it while the model works.
	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		_ = d.rest(typingCtx, "POST", "/channels/"+m.ChannelID+"/typing", nil, nil)
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				_ = d.rest(typingCtx, "POST", "/channels/"+m.ChannelID+"/typing", nil, nil)
			}
		}
	}()

	reply, err := d.mgr.handle(ctx, msg, nil)
	stopTyping()
	if err != nil {
		reply = "⚠️ " + err.Error()
	}
	if strings.TrimSpace(reply) == "" {
		reply = "(no reply)"
	}

	// Post as a normal message rather than a reply-reference: a reply renders
	// the first line inline beside the bot's name, which reads badly for a long
	// answer. A plain message puts the whole reply on its own lines under the
	// name.
	for _, chunk := range splitForDiscord(reply) {
		if _, err := d.Send(ctx, Reply{ChannelID: m.ChannelID, Text: chunk}); err != nil {
			slog.Warn("discord: send failed", "error", err)
			return
		}
	}
}

// Send posts a message and returns its id.
func (d *Discord) Send(ctx context.Context, r Reply) (string, error) {
	payload := map[string]any{
		"content": truncateDC(r.Text),
		// Never ping @everyone or roles from agent output.
		"allowed_mentions": map[string]any{"parse": []string{"users"}},
	}
	if r.ReplyTo != "" {
		payload["message_reference"] = map[string]any{
			"message_id": r.ReplyTo, "fail_if_not_exists": false,
		}
	}

	var result struct {
		ID string `json:"id"`
	}
	if r.EditID != "" {
		err := d.rest(ctx, "PATCH", "/channels/"+r.ChannelID+"/messages/"+r.EditID, payload, &result)
		return r.EditID, err
	}
	err := d.rest(ctx, "POST", "/channels/"+r.ChannelID+"/messages", payload, &result)
	return result.ID, err
}

// Discord caps a message at 2000 characters.
const discordLimit = 1900

func truncateDC(s string) string {
	if len(s) <= discordLimit {
		return s
	}
	return s[:discordLimit] + "…"
}

// splitForDiscord breaks a long reply without cutting fenced code blocks apart.
func splitForDiscord(s string) []string {
	if len(s) <= discordLimit {
		return []string{s}
	}
	var out []string
	for len(s) > discordLimit {
		cut := strings.LastIndex(s[:discordLimit], "\n\n")
		if cut < discordLimit/2 {
			cut = strings.LastIndex(s[:discordLimit], "\n")
		}
		if cut < discordLimit/2 {
			cut = discordLimit
		}
		chunk := strings.TrimSpace(s[:cut])
		// Balance code fences so each chunk renders on its own.
		if strings.Count(chunk, "```")%2 == 1 {
			chunk += "\n```"
			s = "```\n" + strings.TrimSpace(s[cut:])
		} else {
			s = strings.TrimSpace(s[cut:])
		}
		out = append(out, chunk)
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
