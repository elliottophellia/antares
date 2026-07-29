package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/version"
)

// Telegram talks to the Bot API over long polling, so no public domain or
// webhook is required — it works behind a home NAT.
type Telegram struct {
	cfg     config.Telegram
	mgr     *Manager
	client  *http.Client
	baseURL string

	mu        sync.RWMutex
	connected bool
	offset    int64
	me        string
}

// NewTelegram builds the Telegram adapter.
func NewTelegram(cfg config.Telegram, mgr *Manager) *Telegram {
	return &Telegram{
		cfg: cfg, mgr: mgr,
		// The read timeout must exceed the long-poll window.
		client:  &http.Client{Timeout: 90 * time.Second},
		baseURL: "https://api.telegram.org/bot" + cfg.BotToken,
	}
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Connected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connected
}

func (t *Telegram) setConnected(v bool) {
	t.mu.Lock()
	t.connected = v
	t.mu.Unlock()
}

// call performs one Bot API method.
func (t *Telegram) call(ctx context.Context, method string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/"+method, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", version.UserAgent())
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s failed (%d): %s", method, envelope.ErrorCode, envelope.Description)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      tgChat  `json:"chat"`
	Text      string  `json:"text"`
	Caption   string  `json:"caption"`
	Date      int64   `json:"date"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

// Start long-polls for updates until ctx is cancelled.
func (t *Telegram) Start(ctx context.Context) error {
	var me tgUser
	if err := t.call(ctx, "getMe", nil, &me); err != nil {
		return fmt.Errorf("getMe: %w", err)
	}
	t.mu.Lock()
	t.me = me.Username
	token := t.cfg.BotToken
	t.mu.Unlock()
	t.setConnected(true)
	defer t.setConnected(false)
	slog.Info("telegram connected", "bot", "@"+me.Username)

	// Publish the "/" command menu. Off the main path so a hiccup never blocks
	// polling for messages.
	go func() {
		if err := SetTelegramCommands(context.Background(), token); err != nil {
			slog.Warn("telegram: could not set command menu", "error", err)
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		var updates []tgUpdate
		payload := map[string]any{
			"timeout":         50,
			"allowed_updates": []string{"message"},
		}
		if t.offset > 0 {
			payload["offset"] = t.offset
		}
		if err := t.call(ctx, "getUpdates", payload, &updates); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("getUpdates: %w", err)
		}

		for _, u := range updates {
			if u.UpdateID >= t.offset {
				t.offset = u.UpdateID + 1
			}
			if u.Message == nil || u.Message.From == nil || u.Message.From.IsBot {
				continue
			}
			go t.handleMessage(ctx, *u.Message)
		}
	}
}

// handleMessage authorises, runs the agent, and streams the reply back by
// editing a placeholder message.
func (t *Telegram) handleMessage(ctx context.Context, m tgMessage) {
	text := strings.TrimSpace(firstNonEmpty(m.Text, m.Caption))
	if text == "" {
		return
	}

	chatID := strconv.FormatInt(m.Chat.ID, 10)
	userID := strconv.FormatInt(m.From.ID, 10)
	isDirect := m.Chat.Type == "private"

	// In groups the bot only answers when mentioned or commanded.
	if !isDirect {
		t.mu.RLock()
		mention := "@" + t.me
		t.mu.RUnlock()
		if !strings.Contains(text, mention) && !strings.HasPrefix(text, "/") {
			return
		}
		text = strings.TrimSpace(strings.ReplaceAll(text, mention, ""))
	}

	msg := InboundMessage{
		Platform: "telegram", ChannelID: chatID, UserID: userID,
		UserName: firstNonEmpty(m.From.Username, m.From.FirstName),
		Text:     text, IsDirect: isDirect,
		MessageID: strconv.FormatInt(m.MessageID, 10),
	}

	// Pairing is a DM-only handshake: a group chat must never receive a pairing
	// code. In a group, access is governed by the allow-lists and bindings; an
	// un-listed user gets silence, not a code.
	allowed, denial := t.mgr.authorize(ctx, msg, t.cfg.AllowedUsers, t.cfg.AllowedChats, isDirect && t.cfg.RequirePairing)
	if !allowed {
		if denial != "" && isDirect {
			_, _ = t.Send(ctx, Reply{ChannelID: chatID, Text: denial, ReplyTo: msg.MessageID})
		}
		return
	}

	if cmd, handled := t.builtinCommand(ctx, msg, text); handled {
		_, _ = t.Send(ctx, Reply{ChannelID: chatID, Text: cmd, ReplyTo: msg.MessageID})
		return
	}

	_ = t.sendChatAction(ctx, chatID, "typing")

	// A placeholder gives the user immediate feedback; it is edited as the
	// answer streams in, then finalised.
	placeholderID, err := t.Send(ctx, Reply{ChannelID: chatID, Text: "…", ReplyTo: msg.MessageID})
	if err != nil {
		slog.Warn("telegram: cannot send placeholder", "error", err)
		placeholderID = ""
	}

	var (
		mu       sync.Mutex
		lastEdit time.Time
		lastText string
	)
	partial := func(s string) {
		if !t.cfg.StreamEdits || placeholderID == "" || strings.TrimSpace(s) == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		// Telegram rate-limits edits; one per second is comfortably safe.
		if time.Since(lastEdit) < time.Second || s == lastText {
			return
		}
		lastEdit, lastText = time.Now(), s
		_, _ = t.Send(ctx, Reply{ChannelID: chatID, Text: truncateTG(s) + " ▌", EditID: placeholderID})
	}

	reply, err := t.mgr.handle(ctx, msg, partial)
	if err != nil {
		reply = "⚠️ " + err.Error()
	}
	if strings.TrimSpace(reply) == "" {
		reply = "(no reply)"
	}

	for i, chunk := range splitForTelegram(reply) {
		out := Reply{ChannelID: chatID, Text: chunk}
		if i == 0 && placeholderID != "" {
			out.EditID = placeholderID
		} else if i == 0 {
			out.ReplyTo = msg.MessageID
		}
		if _, err := t.Send(ctx, out); err != nil {
			slog.Warn("telegram: send failed", "error", err)
		}
	}
}

// builtinCommand handles slash commands that never reach the model.
func (t *Telegram) builtinCommand(ctx context.Context, msg InboundMessage, text string) (string, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	cmd, _, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	cmd, _, _ = strings.Cut(cmd, "@")

	switch strings.ToLower(cmd) {
	case "start", "help":
		return "*Antares*\n\nSend me anything and I will work on it. I can read files, run commands, " +
			"search the web, and remember what matters across sessions.\n\n" +
			"/new — start a fresh session\n/pair — show the pairing code\n/help — this message", true
	case "pair":
		p, err := t.mgr.db.GetPairing(ctx, "telegram", msg.UserID)
		if err != nil {
			return "No pairing request found. Send any message to create one.", true
		}
		return fmt.Sprintf("Status: %s\nPairing code: %s", p.Status, p.Code), true
	case "new":
		if err := t.mgr.db.DeleteKV(ctx, sessionKey("telegram", msg.ChannelID)); err != nil {
			slog.Debug("telegram: cannot clear session", "error", err)
		}
		return "Started a fresh session.", true
	}
	return "", false
}

func (t *Telegram) sendChatAction(ctx context.Context, chatID, action string) error {
	return t.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

// Send posts or edits a message and returns its id.
func (t *Telegram) Send(ctx context.Context, r Reply) (string, error) {
	payload := map[string]any{
		"chat_id":    r.ChannelID,
		"text":       truncateTG(r.Text),
		"parse_mode": "Markdown",
	}
	method := "sendMessage"
	if r.EditID != "" {
		method = "editMessageText"
		payload["message_id"] = r.EditID
	} else if r.ReplyTo != "" {
		payload["reply_parameters"] = map[string]any{"message_id": r.ReplyTo, "allow_sending_without_reply": true}
	}

	var result tgMessage
	err := t.call(ctx, method, payload, &result)
	if err != nil && strings.Contains(err.Error(), "can't parse entities") {
		// The model produced Markdown Telegram rejects; resend as plain text.
		delete(payload, "parse_mode")
		err = t.call(ctx, method, payload, &result)
	}
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(result.MessageID, 10), nil
}

// Telegram caps a message at 4096 characters.
const telegramLimit = 4000

func truncateTG(s string) string {
	if len(s) <= telegramLimit {
		return s
	}
	return s[:telegramLimit] + "…"
}

// splitForTelegram breaks a long reply on paragraph boundaries where possible.
func splitForTelegram(s string) []string {
	if len(s) <= telegramLimit {
		return []string{s}
	}
	var out []string
	for len(s) > telegramLimit {
		cut := strings.LastIndex(s[:telegramLimit], "\n\n")
		if cut < telegramLimit/2 {
			cut = strings.LastIndex(s[:telegramLimit], "\n")
		}
		if cut < telegramLimit/2 {
			cut = telegramLimit
		}
		out = append(out, strings.TrimSpace(s[:cut]))
		s = strings.TrimSpace(s[cut:])
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// sessionKey maps a platform channel to its persistent session id.
func sessionKey(platform, channel string) string {
	return "gateway_session:" + platform + ":" + channel
}
