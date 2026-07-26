// Package gateway connects Antares to messaging platforms. Every platform
// shares the same session store, memory, and tool surface.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

// InboundMessage is one message received from a platform.
type InboundMessage struct {
	Platform  string
	ChannelID string
	UserID    string
	UserName  string
	Text      string
	// IsDirect marks a 1:1 chat, where the agent replies without being addressed.
	IsDirect bool
	// MessageID lets adapters edit their own reply while streaming.
	MessageID string
}

// Reply is what an adapter sends back.
type Reply struct {
	ChannelID string
	Text      string
	// ReplyTo threads the response onto the triggering message when supported.
	ReplyTo string
	// EditID updates a previously sent message instead of posting a new one.
	EditID string
}

// Handler processes an inbound message and streams partial replies.
// The returned string is the final text.
type Handler func(ctx context.Context, msg InboundMessage, partial func(string)) (string, error)

// Adapter is one platform connection.
type Adapter interface {
	Name() string
	// Start blocks until ctx is cancelled or the connection fails fatally.
	Start(ctx context.Context) error
	// Send delivers a message; it returns the platform message id.
	Send(ctx context.Context, r Reply) (string, error)
	// Connected reports the live connection state for the dashboard.
	Connected() bool
}

// Manager supervises the enabled adapters.
type Manager struct {
	cfg     *config.Config
	db      store.Store
	handler Handler

	mu       sync.RWMutex
	adapters map[string]Adapter
	cancels  map[string]context.CancelFunc
}

// NewManager builds a gateway manager.
func NewManager(cfg *config.Config, db store.Store, handler Handler) *Manager {
	return &Manager{
		cfg: cfg, db: db, handler: handler,
		adapters: map[string]Adapter{},
		cancels:  map[string]context.CancelFunc{},
	}
}

// Start launches every enabled platform and keeps it running until ctx ends.
func (m *Manager) Start(ctx context.Context) {
	if !m.cfg.Gateway.Enabled {
		slog.Debug("gateway disabled")
		return
	}
	if tg := m.cfg.Gateway.Telegram; tg.Enabled && tg.BotToken != "" {
		m.startAdapter(ctx, NewTelegram(tg, m))
	}
	if dc := m.cfg.Gateway.Discord; dc.Enabled && dc.BotToken != "" {
		m.startAdapter(ctx, NewDiscord(dc, m))
	}
}

// startAdapter runs one adapter with restart-on-failure backoff.
func (m *Manager) startAdapter(ctx context.Context, a Adapter) {
	adapterCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.adapters[a.Name()] = a
	m.cancels[a.Name()] = cancel
	m.mu.Unlock()

	go func() {
		backoff := time.Second
		for {
			if adapterCtx.Err() != nil {
				return
			}
			slog.Info("gateway connecting", "platform", a.Name())
			err := a.Start(adapterCtx)
			if adapterCtx.Err() != nil {
				return
			}
			if err != nil {
				slog.Error("gateway disconnected", "platform", a.Name(), "error", err, "retry_in", backoff)
			}
			select {
			case <-adapterCtx.Done():
				return
			case <-time.After(backoff):
			}
			// Exponential backoff, capped so a long outage still recovers promptly.
			if backoff < 2*time.Minute {
				backoff *= 2
			}
		}
	}()
}

// Stop shuts down one platform.
func (m *Manager) Stop(name string) {
	m.mu.Lock()
	cancel, ok := m.cancels[name]
	delete(m.cancels, name)
	delete(m.adapters, name)
	m.mu.Unlock()
	if ok {
		cancel()
	}
}

// StopAll shuts down every platform.
func (m *Manager) StopAll() {
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.cancels))
	for _, c := range m.cancels {
		cancels = append(cancels, c)
	}
	m.cancels = map[string]context.CancelFunc{}
	m.adapters = map[string]Adapter{}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// Status reports each platform's connection state.
func (m *Manager) Status() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]bool, len(m.adapters))
	for name, a := range m.adapters {
		out[name] = a.Connected()
	}
	return out
}

// Deliver sends text to "platform:channel", used by the cron scheduler.
func (m *Manager) Deliver(ctx context.Context, target, text string) error {
	platform, channel, ok := strings.Cut(target, ":")
	if !ok || channel == "" {
		return fmt.Errorf("delivery target must look like platform:channel, got %q", target)
	}
	m.mu.RLock()
	a, exists := m.adapters[platform]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("platform %q is not connected", platform)
	}
	_, err := a.Send(ctx, Reply{ChannelID: channel, Text: text})
	return err
}

// authorize applies the allow lists and the pairing flow.
// It returns the reply to send when access is refused.
func (m *Manager) authorize(ctx context.Context, msg InboundMessage, allowedUsers, allowedChats []string, requirePairing bool) (bool, string) {
	if len(allowedUsers) > 0 && contains(allowedUsers, msg.UserID) {
		return true, ""
	}
	if len(allowedChats) > 0 && contains(allowedChats, msg.ChannelID) {
		return true, ""
	}
	// An explicit allow list with no match is a hard denial.
	if len(allowedUsers) > 0 || len(allowedChats) > 0 {
		return false, ""
	}
	if !requirePairing {
		return true, ""
	}

	pairing, err := m.db.GetPairing(ctx, msg.Platform, msg.UserID)
	if err == nil && pairing.Status == "approved" {
		return true, ""
	}

	// First contact: record a pending pairing so the dashboard can approve it.
	if err != nil {
		code := pairingCode()
		p := &store.Pairing{
			ID: newID("pair"), Platform: msg.Platform, ExternalID: msg.UserID,
			DisplayName: msg.UserName, Status: "pending", Code: code,
		}
		if err := m.db.PutPairing(ctx, p); err != nil {
			slog.Warn("gateway: cannot record pairing", "error", err)
		}
		slog.Info("gateway pairing requested", "platform", msg.Platform, "user", msg.UserName, "code", code)
		return false, fmt.Sprintf(
			"This chat is not linked yet. Approve it in the Antares dashboard under Channels.\n\nPairing code: %s", code)
	}
	if pairing.Status == "revoked" {
		return false, "Access to this Antares instance has been revoked."
	}
	return false, fmt.Sprintf(
		"Waiting for approval in the Antares dashboard.\n\nPairing code: %s", pairing.Code)
}

// handle runs the agent for an inbound message and returns the reply text.
func (m *Manager) handle(ctx context.Context, msg InboundMessage, partial func(string)) (string, error) {
	if m.handler == nil {
		return "", fmt.Errorf("no handler configured")
	}
	return m.handler(ctx, msg, partial)
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), want) {
			return true
		}
	}
	return false
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func pairingCode() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}
