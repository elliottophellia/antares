package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// Matrix connects to a homeserver with an access token and long-polls /sync.
// Replies are sent with PUT .../send. It needs no public URL.
type Matrix struct {
	cfg    config.Matrix
	mgr    *Manager
	client *http.Client
	base   string

	mu        sync.RWMutex
	connected bool
	txn       int64
}

// NewMatrix builds the Matrix adapter.
func NewMatrix(cfg config.Matrix, mgr *Manager) *Matrix {
	base := strings.TrimRight(cfg.Homeserver, "/")
	return &Matrix{cfg: cfg, mgr: mgr, base: base, client: &http.Client{Timeout: 60 * time.Second}}
}

func (m *Matrix) Name() string { return "matrix" }

func (m *Matrix) Connected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

func (m *Matrix) setConnected(v bool) {
	m.mu.Lock()
	m.connected = v
	m.mu.Unlock()
}

// Start syncs from "now" (skipping history) and processes new messages until
// the context is cancelled.
func (m *Matrix) Start(ctx context.Context) error {
	if m.base == "" || m.cfg.AccessToken == "" {
		return fmt.Errorf("matrix needs homeserver and access_token")
	}
	// First sync with no since token, but full_state=false, to get the current
	// batch token without replaying old messages.
	since, err := m.initialSync(ctx)
	if err != nil {
		return err
	}
	m.setConnected(true)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, msgs, err := m.sync(ctx, since)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.setConnected(false)
			slog.Warn("matrix sync failed, retrying", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		m.setConnected(true)
		since = next
		for _, msg := range msgs {
			m.dispatch(ctx, msg)
		}
	}
}

type mxMessage struct {
	Room    string
	Sender  string
	Body    string
	EventID string
}

// initialSync returns the current next_batch without any room timelines.
func (m *Matrix) initialSync(ctx context.Context) (string, error) {
	u := fmt.Sprintf("%s/_matrix/client/v3/sync?timeout=0", m.base)
	var out struct {
		NextBatch string `json:"next_batch"`
	}
	if err := m.get(ctx, u, &out); err != nil {
		return "", err
	}
	return out.NextBatch, nil
}

// sync long-polls for new events since the given batch token.
func (m *Matrix) sync(ctx context.Context, since string) (string, []mxMessage, error) {
	u := fmt.Sprintf("%s/_matrix/client/v3/sync?timeout=30000&since=%s", m.base, url.QueryEscape(since))
	var out struct {
		NextBatch string `json:"next_batch"`
		Rooms     struct {
			Join map[string]struct {
				Timeline struct {
					Events []struct {
						Type    string `json:"type"`
						Sender  string `json:"sender"`
						EventID string `json:"event_id"`
						Content struct {
							MsgType string `json:"msgtype"`
							Body    string `json:"body"`
						} `json:"content"`
					} `json:"events"`
				} `json:"timeline"`
			} `json:"join"`
		} `json:"rooms"`
	}
	if err := m.get(ctx, u, &out); err != nil {
		return since, nil, err
	}
	var msgs []mxMessage
	for roomID, room := range out.Rooms.Join {
		for _, ev := range room.Timeline.Events {
			if ev.Type != "m.room.message" || ev.Content.MsgType != "m.text" {
				continue
			}
			if ev.Sender == m.cfg.UserID {
				continue // our own messages
			}
			msgs = append(msgs, mxMessage{Room: roomID, Sender: ev.Sender, Body: ev.Content.Body, EventID: ev.EventID})
		}
	}
	return out.NextBatch, msgs, nil
}

func (m *Matrix) dispatch(ctx context.Context, mx mxMessage) {
	text := strings.TrimSpace(mx.Body)
	if text == "" {
		return
	}
	msg := InboundMessage{
		Platform: "matrix", ChannelID: mx.Room, UserID: mx.Sender, UserName: mx.Sender,
		Text: text, IsDirect: false, MessageID: mx.EventID,
	}
	if ok, denial := m.mgr.authorize(ctx, msg, m.cfg.AllowedUsers, m.cfg.AllowedRooms, m.cfg.RequirePairing); !ok {
		if denial != "" {
			_, _ = m.Send(ctx, Reply{ChannelID: mx.Room, Text: denial})
		}
		return
	}
	final, err := m.mgr.handle(ctx, msg, func(string) {})
	if err != nil {
		final = "Sorry — something went wrong: " + err.Error()
	}
	if strings.TrimSpace(final) == "" {
		final = "(no reply)"
	}
	_, _ = m.Send(ctx, Reply{ChannelID: mx.Room, Text: final})
}

// Send posts a message to a room. Matrix has no cheap edit, so EditID is
// ignored and a new message is sent.
func (m *Matrix) Send(ctx context.Context, r Reply) (string, error) {
	m.mu.Lock()
	m.txn++
	txn := m.txn
	m.mu.Unlock()

	u := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%d",
		m.base, url.PathEscape(r.ChannelID), txn)
	body := map[string]string{"msgtype": "m.text", "body": r.Text}
	var out struct {
		EventID string `json:"event_id"`
	}
	if err := m.put(ctx, u, body, &out); err != nil {
		return "", err
	}
	return out.EventID, nil
}

func (m *Matrix) get(ctx context.Context, url string, out any) error {
	return m.do(ctx, "GET", url, nil, out)
}
func (m *Matrix) put(ctx context.Context, url string, body, out any) error {
	return m.do(ctx, "PUT", url, body, out)
}

func (m *Matrix) do(ctx context.Context, method, url string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.cfg.AccessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("matrix %s: HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
