package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// Signal talks to a signal-cli REST API daemon that the user runs. It polls the
// receive endpoint and posts to send, so it needs no public URL of its own.
type Signal struct {
	cfg    config.Signal
	mgr    *Manager
	client *http.Client
	base   string

	mu        sync.RWMutex
	connected bool
}

// NewSignal builds the Signal adapter.
func NewSignal(cfg config.Signal, mgr *Manager) *Signal {
	return &Signal{cfg: cfg, mgr: mgr, base: strings.TrimRight(cfg.APIURL, "/"),
		client: &http.Client{Timeout: 40 * time.Second}}
}

func (s *Signal) Name() string { return "signal" }

func (s *Signal) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

func (s *Signal) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

// Start polls the receive endpoint until the context is cancelled.
func (s *Signal) Start(ctx context.Context) error {
	if s.base == "" || s.cfg.Number == "" {
		return fmt.Errorf("signal needs api_url and number")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msgs, err := s.receive(ctx)
		if err != nil {
			s.setConnected(false)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		s.setConnected(true)
		for _, m := range msgs {
			s.dispatch(ctx, m)
		}
		// The receive endpoint returns promptly; pace the polling.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

type sigMessage struct {
	Source string
	Group  string
	Body   string
}

// receive fetches and flattens pending messages.
func (s *Signal) receive(ctx context.Context) ([]sigMessage, error) {
	u := fmt.Sprintf("%s/v1/receive/%s", s.base, s.cfg.Number)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("signal receive HTTP %d", resp.StatusCode)
	}
	return parseSignalReceive(data), nil
}

// parseSignalReceive extracts text messages from a signal-cli receive payload.
func parseSignalReceive(data []byte) []sigMessage {
	var envs []struct {
		Envelope struct {
			Source      string `json:"source"`
			DataMessage *struct {
				Message   string `json:"message"`
				GroupInfo *struct {
					GroupID string `json:"groupId"`
				} `json:"groupInfo"`
			} `json:"dataMessage"`
		} `json:"envelope"`
	}
	if json.Unmarshal(data, &envs) != nil {
		return nil
	}
	var out []sigMessage
	for _, e := range envs {
		dm := e.Envelope.DataMessage
		if dm == nil || strings.TrimSpace(dm.Message) == "" {
			continue
		}
		m := sigMessage{Source: e.Envelope.Source, Body: dm.Message}
		if dm.GroupInfo != nil {
			m.Group = dm.GroupInfo.GroupID
		}
		out = append(out, m)
	}
	return out
}

func (s *Signal) dispatch(ctx context.Context, m sigMessage) {
	// Reply target: the group when present, else the sender directly.
	channel := m.Source
	direct := true
	if m.Group != "" {
		channel = "group." + m.Group
		direct = false
	}
	msg := InboundMessage{
		Platform: "signal", ChannelID: channel, UserID: m.Source, UserName: m.Source,
		Text: strings.TrimSpace(m.Body), IsDirect: direct,
	}
	if ok, denial := s.mgr.authorize(ctx, msg, s.cfg.AllowedUsers, nil, s.cfg.RequirePairing); !ok {
		if denial != "" {
			_, _ = s.Send(ctx, Reply{ChannelID: channel, Text: denial})
		}
		return
	}
	final, err := s.mgr.handle(ctx, msg, func(string) {})
	if err != nil {
		final = "Sorry — something went wrong: " + err.Error()
	}
	if strings.TrimSpace(final) == "" {
		final = "(no reply)"
	}
	_, _ = s.Send(ctx, Reply{ChannelID: channel, Text: final})
}

// Send posts a message to a recipient or group.
func (s *Signal) Send(ctx context.Context, r Reply) (string, error) {
	body := map[string]any{"message": r.Text, "number": s.cfg.Number}
	if strings.HasPrefix(r.ChannelID, "group.") {
		body["recipients"] = []string{strings.TrimPrefix(r.ChannelID, "group.")}
	} else {
		body["recipients"] = []string{r.ChannelID}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.base+"/v2/send", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("signal send HTTP %d", resp.StatusCode)
	}
	return "", nil
}
