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

const whatsappGraph = "https://graph.facebook.com/v20.0"

// WhatsApp uses the Meta Cloud API. Inbound messages arrive by webhook, so the
// adapter runs its own HTTP listener; outbound goes to the Graph API. The user
// points Meta's webhook at this listener (directly or through a reverse proxy).
type WhatsApp struct {
	cfg    config.WhatsApp
	mgr    *Manager
	client *http.Client

	mu        sync.RWMutex
	connected bool
}

// NewWhatsApp builds the WhatsApp adapter.
func NewWhatsApp(cfg config.WhatsApp, mgr *Manager) *WhatsApp {
	if cfg.Path == "" {
		cfg.Path = "/webhook"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8090"
	}
	return &WhatsApp{cfg: cfg, mgr: mgr, client: &http.Client{Timeout: 30 * time.Second}}
}

func (w *WhatsApp) Name() string { return "whatsapp" }

func (w *WhatsApp) Connected() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.connected
}

// Start serves the webhook until the context is cancelled.
func (w *WhatsApp) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(w.cfg.Path, w.handleWebhook(ctx))
	srv := &http.Server{Addr: w.cfg.ListenAddr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	w.mu.Lock()
	w.connected = true
	w.mu.Unlock()
	slog.Info("whatsapp webhook listening", "addr", w.cfg.ListenAddr, "path", w.cfg.Path)

	err := srv.ListenAndServe()
	w.mu.Lock()
	w.connected = false
	w.mu.Unlock()
	if err == http.ErrServerClosed {
		return ctx.Err()
	}
	return err
}

func (w *WhatsApp) handleWebhook(ctx context.Context) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Meta's verification handshake.
			q := r.URL.Query()
			if q.Get("hub.mode") == "subscribe" && q.Get("hub.verify_token") == w.cfg.VerifyToken {
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write([]byte(q.Get("hub.challenge")))
				return
			}
			rw.WriteHeader(http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		rw.WriteHeader(http.StatusOK) // ack fast; Meta retries otherwise
		for _, m := range parseWhatsAppWebhook(body) {
			go w.dispatch(ctx, m)
		}
	}
}

type waMessage struct {
	From string
	Body string
}

// parseWhatsAppWebhook extracts inbound text messages from a Cloud API payload.
func parseWhatsAppWebhook(data []byte) []waMessage {
	var p struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						From string `json:"from"`
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	var out []waMessage
	for _, e := range p.Entry {
		for _, c := range e.Changes {
			for _, m := range c.Value.Messages {
				if m.Type != "text" || strings.TrimSpace(m.Text.Body) == "" {
					continue
				}
				out = append(out, waMessage{From: m.From, Body: m.Text.Body})
			}
		}
	}
	return out
}

func (w *WhatsApp) dispatch(ctx context.Context, m waMessage) {
	msg := InboundMessage{
		Platform: "whatsapp", ChannelID: m.From, UserID: m.From, UserName: m.From,
		Text: strings.TrimSpace(m.Body), IsDirect: true,
	}
	if ok, denial := w.mgr.authorize(ctx, msg, w.cfg.AllowedUsers, nil, false); !ok {
		if denial != "" {
			_, _ = w.Send(ctx, Reply{ChannelID: m.From, Text: denial})
		}
		return
	}
	final, err := w.mgr.handle(ctx, msg, func(string) {})
	if err != nil {
		final = "Sorry — something went wrong: " + err.Error()
	}
	if strings.TrimSpace(final) == "" {
		final = "(no reply)"
	}
	_, _ = w.Send(ctx, Reply{ChannelID: m.From, Text: final})
}

// Send posts a text message through the Graph API.
func (w *WhatsApp) Send(ctx context.Context, r Reply) (string, error) {
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                r.ChannelID,
		"type":              "text",
		"text":              map[string]string{"body": r.Text},
	}
	b, _ := json.Marshal(body)
	u := fmt.Sprintf("%s/%s/messages", whatsappGraph, w.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("whatsapp send HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return "", nil
}
