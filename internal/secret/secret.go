// Package secret encrypts small at-rest values (VPS passwords and SSH keys)
// with a machine-local key, so a leaked database does not hand over the
// credentials it stores.
//
// The key lives in ~/.antares/.secretkey (0600), generated on first use. This
// protects against a database read alone; someone with the key file and the DB
// can still decrypt, which is the right trade-off for a single-machine tool —
// the key never leaves the box and is not in the DB.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Box encrypts and decrypts with a single AES-256-GCM key.
type Box struct {
	aead cipher.AEAD
}

var (
	def     *Box
	defErr  error
	defOnce sync.Once
)

// Default returns the process-wide Box keyed from ~/.antares/.secretkey,
// creating the key on first use. Subsequent calls return the same Box.
func Default() (*Box, error) {
	defOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			defErr = err
			return
		}
		def, defErr = Open(filepath.Join(home, ".antares", ".secretkey"))
	})
	return def, defErr
}

// Open loads (or creates) the key at keyPath and returns a Box.
func Open(keyPath string) (*Box, error) {
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// NewBox builds a Box from a raw 32-byte key (used in tests).
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt returns a base64 string of nonce||ciphertext. An empty input encrypts
// to an empty string so blank fields stay blank.
func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt reverses Encrypt. An empty input returns an empty string.
func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (wrong key or corrupt data): %w", err)
	}
	return string(plain), nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		key, err := base64.StdEncoding.DecodeString(string(raw))
		if err == nil && len(key) == 32 {
			return key, nil
		}
		// A corrupt key file is fatal: silently regenerating would orphan every
		// value already encrypted with the old key.
		return nil, fmt.Errorf("secret key at %s is corrupt", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
