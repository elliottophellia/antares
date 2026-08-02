package secret

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SocialKey is the master encryption key for Social Media credentials. It is
// loaded from a managed env file (~/.antares/secrets.env) or the
// ANTARES_MASTER_KEY environment variable. The key never lives in the database,
// config YAML, API responses, logs, prompts, RAG, or browser profiles.
type SocialKey struct {
	key []byte
}

var (
	socialKey     *SocialKey
	socialKeyErr  error
	socialKeyOnce sync.Once
)

// SocialDefault loads the social master key from the managed env file or the
// ANTARES_MASTER_KEY environment variable. It does NOT generate one
// automatically — Social Media encryption is opt-in, not silent.
func SocialDefault() (*SocialKey, error) {
	socialKeyOnce.Do(func() {
		// Environment variable takes precedence (container/Docker deployments).
		if env := strings.TrimSpace(os.Getenv("ANTARES_MASTER_KEY")); env != "" {
			socialKey, socialKeyErr = parseSocialKey(env)
			return
		}
		// Managed env file.
		home, err := os.UserHomeDir()
		if err != nil {
			socialKeyErr = err
			return
		}
		path := filepath.Join(home, ".antares", "secrets.env")
		socialKey, socialKeyErr = loadSocialKeyFile(path)
	})
	return socialKey, socialKeyErr
}

// SocialReset clears the cached key so the next call re-reads from disk/env.
// Used after onboarding generates a new key.
func SocialReset() {
	socialKeyOnce = sync.Once{}
}

// SocialAvailable reports whether a SocialKey can be loaded right now without
// generating. It does not fail the process.
func SocialAvailable() bool {
	_, err := SocialDefault()
	return err == nil
}

// Box returns a Box backed by the social master key.
func (k *SocialKey) Box() (*Box, error) {
	if len(k.key) != 32 {
		return nil, fmt.Errorf("social master key must be 32 bytes, got %d", len(k.key))
	}
	return NewBox(k.key)
}

// SocialKeyPath returns the managed env file path.
func SocialKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".antares/secrets.env"
	}
	return filepath.Join(home, ".antares", "secrets.env")
}

// SocialGenerateKey creates a new 32-byte master key, writes it to the managed
// env file with 0600 permissions, and returns the raw key for one-time display.
// If the file already exists and contains a valid key, it is returned without
// regeneration.
func SocialGenerateKey() ([]byte, error) {
	path := SocialKeyPath()

	// If the file already has a valid key, return it.
	if existing, err := loadSocialKeyFile(path); err == nil {
		return existing.key, nil
	}

	// Generate a fresh key.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(key)
	content := fmt.Sprintf("ANTARES_MASTER_KEY=%s\n", encoded)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secrets dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return nil, fmt.Errorf("write secrets file: %w", err)
	}

	// Clear the cache so the next call picks up the new key.
	SocialReset()

	return key, nil
}

func parseSocialKey(raw string) (*SocialKey, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("ANTARES_MASTER_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ANTARES_MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	return &SocialKey{key: key}, nil
}

func loadSocialKeyFile(path string) (*SocialKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("social master key not found at %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ANTARES_MASTER_KEY=") {
			raw := strings.TrimPrefix(line, "ANTARES_MASTER_KEY=")
			return parseSocialKey(raw)
		}
	}
	return nil, fmt.Errorf("ANTARES_MASTER_KEY not found in %s", path)
}
