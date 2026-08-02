// Package socialbrowser manages the persistent stealth Chromium used by the
// Social Media agent. It wraps cloak-go to provide a singleton browser with
// a stable fingerprint seed and user-data-dir so cookies, localStorage, and
// login sessions survive across restarts.
package socialbrowser

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// State describes the browser's current runtime status.
type State string

const (
	StateDisabled     State = "disabled"
	StateUnavailable  State = "unavailable"
	StateStopped      State = "stopped"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateError        State = "error"
)

// Manager owns one persistent social media browser. Only one controller may
// drive the browser at a time; a mutex serializes start/stop/control calls.
type Manager struct {
	mu       sync.Mutex
	state    State
	errMsg   string
	browser  interface{ Close() } // *cloak.Browser when launched
	cancel   context.CancelFunc
	profileDir string
	seedFile   string
}

// New creates a Manager. The profile directory lives under the Antares home.
func New() *Manager {
	profileDir := config.Path("social-browser", "profile")
	seedFile := config.Path("social-browser", "seed")
	return &Manager{
		state:      StateStopped,
		profileDir: profileDir,
		seedFile:   seedFile,
	}
}

// Status returns the current browser state and any error detail.
func (m *Manager) Status() (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.state), m.errMsg
}

// Start launches the persistent browser if it is not already running.
// This is synchronous but may take up to 45 seconds on first launch
// (binary download + verification). The caller should run it in a goroutine.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.state == StateRunning {
		m.mu.Unlock()
		return nil
	}
	if m.state == StateStarting {
		m.mu.Unlock()
		return fmt.Errorf("browser is already starting")
	}
	m.state = StateStarting
	m.errMsg = ""
	m.mu.Unlock()

	// Ensure profile and seed directories exist.
	if err := os.MkdirAll(m.profileDir, 0o700); err != nil {
		m.fail("cannot create profile directory: %v", err)
		return err
	}

	// Load or create a stable fingerprint seed.
	seed, err := m.loadOrCreateSeed()
	if err != nil {
		m.fail("cannot initialize fingerprint seed: %v", err)
		return err
	}

	// Launch the browser via cloak-go. The import is deferred to avoid a
	// hard dependency when the social feature is not used.
	browser, cancel, err := m.launchCloak(ctx, seed)
	if err != nil {
		m.fail("browser launch failed: %v", err)
		return err
	}

	m.mu.Lock()
	m.browser = browser
	m.cancel = cancel
	m.state = StateRunning
	m.mu.Unlock()
	return nil
}

// Stop closes the browser if it is running.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.state != StateRunning && m.state != StateStarting {
		m.mu.Unlock()
		return
	}
	browser := m.browser
	cancel := m.cancel
	m.browser = nil
	m.cancel = nil
	m.state = StateStopped
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if browser != nil {
		browser.Close()
	}
}

// fail sets the error state with a formatted message.
func (m *Manager) fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	m.mu.Lock()
	m.state = StateError
	m.errMsg = msg
	m.mu.Unlock()
}

// loadOrCreateSeed reads or generates the persistent fingerprint seed.
// The seed is a random base64 string stored in a file with 0600 perms.
func (m *Manager) loadOrCreateSeed() (string, error) {
	if raw, err := os.ReadFile(m.seedFile); err == nil {
		s := string(raw)
		if s != "" {
			return s, nil
		}
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return "", err
	}
	s := base64.StdEncoding.EncodeToString(seed)
	if err := os.MkdirAll(filepath.Dir(m.seedFile), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(m.seedFile, []byte(s), 0o600); err != nil {
		return "", err
	}
	return s, nil
}

// ProfileDir returns the persistent browser profile path.
func (m *Manager) ProfileDir() string { return m.profileDir }

// Close stops the browser if running. Called on shutdown.
func (m *Manager) Close() {
	m.Stop()
}

// launchCloak launches the stealth browser via cloak-go. This is isolated so
// the cloak-go import is only needed when the feature is actually used.
func (m *Manager) launchCloak(ctx context.Context, seed string) (interface{ Close() }, context.CancelFunc, error) {
	return launchCloakBrowser(ctx, m.profileDir, seed)
}

// launchCloakBrowser is the actual cloak-go integration point. It is in a
// separate file so that the cloak-go import can be managed.
var launchCloakBrowser func(ctx context.Context, profileDir, seed string) (interface{ Close() }, context.CancelFunc, error) = defaultLaunchCloak

// defaultLaunchCloak is a placeholder that returns an error if cloak-go is
// not available. The real implementation is in cloak_launcher.go.
func defaultLaunchCloak(ctx context.Context, profileDir, seed string) (interface{ Close() }, context.CancelFunc, error) {
	return nil, nil, fmt.Errorf("social browser backend not configured")
}

// WaitForRunning polls the browser state until it is running or the timeout
// expires. Used by API handlers that started the browser asynchronously.
func (m *Manager) WaitForRunning(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, _ := m.Status()
		if state == string(StateRunning) {
			return true
		}
		if state == string(StateError) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
