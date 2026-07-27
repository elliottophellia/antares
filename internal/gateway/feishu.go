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
)

const feishuAPI = "https://open.feishu.cn/open-apis"

// Feishu (Lark) receives events by webhook and sends with a tenant token. The
// adapter runs its own listener and refreshes the tenant token as needed. It
// handles plaintext (unencrypted) event delivery.
type Feishu struct {
	cfg    config.Feishu
	mgr    *Manager
	client *http.Client

	mu        sync.RWMutex
	connected bool
	token     string
	tokenExp  time.Time
}

// NewFeishu builds the Feishu adapter.
func NewFeishu(cfg config.Feishu, mgr *Manager) *Feishu {
	if cfg.Path == "" {
		cfg.Path = "/webhook"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8091"
	}
	return &Feishu{cfg: cfg, mgr: mgr, client: &http.Client{Timeout: 30 * time.Second}}
}

func (f *Feishu) Name() string { return "feishu" }

func (f *Feishu) Connected() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.connected
}

// Start serves the event webhook until the context is cancelled.
func (f *Feishu) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(f.cfg.Path, f.handleWebhook(ctx))
	srv := &http.Server{Addr: f.cfg.ListenAddr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	f.mu.Lock()
	f.connected = true
	f.mu.Unlock()
	slog.Info("feishu webhook listening", "addr", f.cfg.ListenAddr, "path", f.cfg.Path)

	err := srv.ListenAndServe()
	f.mu.Lock()
	f.connected = false
	f.mu.Unlock()
	if err == http.ErrServerClosed {
		return ctx.Err()
	}
	return err
}

func (f *Feishu) handleWebhook(ctx context.Context) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))

		// URL verification handshake.
		var probe struct {
			Type      string `json:"type"`
			Challenge string `json:"challenge"`
			Token     string `json:"token"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Type == "url_verification" {
			if f.cfg.VerifyToken != "" && probe.Token != f.cfg.VerifyToken {
				rw.WriteHeader(http.StatusForbidden)
				return
			}
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(fmt.Sprintf(`{"challenge":%q}`, probe.Challenge)))
			return
		}

		rw.WriteHeader(http.StatusOK) // ack fast
		if msg, ok := parseFeishuEvent(body); ok {
			go f.dispatch(ctx, msg)
		}
	}
}

type fsMessage struct {
	ChatID string
	Sender string
	Body   string
}

// parseFeishuEvent extracts a text message from a v2 event payload.
func parseFeishuEvent(data []byte) (fsMessage, bool) {
	var p struct {
		Header struct {
			EventType string `json:"event_type"`
		} `json:"header"`
		Event struct {
			Sender struct {
				SenderID struct {
					OpenID string `json:"open_id"`
				} `json:"sender_id"`
			} `json:"sender"`
			Message struct {
				ChatID      string `json:"chat_id"`
				MessageType string `json:"message_type"`
				Content     string `json:"content"`
			} `json:"message"`
		} `json:"event"`
	}
	if json.Unmarshal(data, &p) != nil {
		return fsMessage{}, false
	}
	if p.Header.EventType != "im.message.receive_v1" || p.Event.Message.MessageType != "text" {
		return fsMessage{}, false
	}
	var content struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(p.Event.Message.Content), &content) != nil || strings.TrimSpace(content.Text) == "" {
		return fsMessage{}, false
	}
	return fsMessage{
		ChatID: p.Event.Message.ChatID,
		Sender: p.Event.Sender.SenderID.OpenID,
		Body:   content.Text,
	}, true
}

func (f *Feishu) dispatch(ctx context.Context, m fsMessage) {
	msg := InboundMessage{
		Platform: "feishu", ChannelID: m.ChatID, UserID: m.Sender, UserName: m.Sender,
		Text: strings.TrimSpace(m.Body), IsDirect: false,
	}
	if ok, denial := f.mgr.authorize(ctx, msg, f.cfg.AllowedUsers, f.cfg.AllowedChats, false); !ok {
		if denial != "" {
			_, _ = f.Send(ctx, Reply{ChannelID: m.ChatID, Text: denial})
		}
		return
	}
	final, err := f.mgr.handle(ctx, msg, func(string) {})
	if err != nil {
		final = "Sorry — something went wrong: " + err.Error()
	}
	if strings.TrimSpace(final) == "" {
		final = "(no reply)"
	}
	_, _ = f.Send(ctx, Reply{ChannelID: m.ChatID, Text: final})
}

// Send posts a text message to a chat.
func (f *Feishu) Send(ctx context.Context, r Reply) (string, error) {
	tok, err := f.tenantToken(ctx)
	if err != nil {
		return "", err
	}
	content, _ := json.Marshal(map[string]string{"text": r.Text})
	body, _ := json.Marshal(map[string]any{
		"receive_id": r.ChannelID,
		"msg_type":   "text",
		"content":    string(content),
	})
	u := feishuAPI + "/im/v1/messages?receive_id_type=chat_id"
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("feishu send HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return "", nil
}

// tenantToken returns a valid tenant access token, refreshing before expiry.
func (f *Feishu) tenantToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.tokenExp.Add(-1*time.Minute)) {
		return f.token, nil
	}
	body, _ := json.Marshal(map[string]string{"app_id": f.cfg.AppID, "app_secret": f.cfg.AppSecret})
	req, err := http.NewRequestWithContext(ctx, "POST", feishuAPI+"/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu token error %d: %s", out.Code, out.Msg)
	}
	f.token = out.TenantAccessToken
	f.tokenExp = time.Now().Add(time.Duration(out.Expire) * time.Second)
	return f.token, nil
}
