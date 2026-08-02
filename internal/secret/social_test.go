package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSocialGenerateKeyCreatesValidFile(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
	os.Setenv("HOME", dir)
	SocialReset()

	key, err := SocialGenerateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key len = %d, want 32", len(key))
	}

	path := filepath.Join(dir, ".antares", "secrets.env")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file perm = %o, want 0600", info.Mode().Perm())
	}
}

func TestSocialDefaultLoadsFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origEnv := os.Getenv("ANTARES_MASTER_KEY")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		os.Setenv("ANTARES_MASTER_KEY", origEnv)
		SocialReset()
	})
	os.Setenv("HOME", dir)
	os.Setenv("ANTARES_MASTER_KEY", "")

	if _, err := SocialGenerateKey(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	SocialReset()

	key, err := SocialDefault()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	box, err := key.Box()
	if err != nil {
		t.Fatalf("box: %v", err)
	}

	ct, err := box.Encrypt("hello")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pt, err := box.Decrypt(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if pt != "hello" {
		t.Fatalf("roundtrip = %q, want hello", pt)
	}
}

func TestSocialDefaultFromEnvVar(t *testing.T) {
	origEnv := os.Getenv("ANTARES_MASTER_KEY")
	t.Cleanup(func() {
		os.Setenv("ANTARES_MASTER_KEY", origEnv)
		SocialReset()
	})
	os.Setenv("ANTARES_MASTER_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	SocialReset()

	key, err := SocialDefault()
	if err != nil {
		t.Fatalf("load from env: %v", err)
	}
	if key == nil {
		t.Fatal("key is nil")
	}
}

func TestSocialAvailableFalseWithoutKey(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origEnv := os.Getenv("ANTARES_MASTER_KEY")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		os.Setenv("ANTARES_MASTER_KEY", origEnv)
		SocialReset()
	})
	os.Setenv("HOME", dir)
	os.Setenv("ANTARES_MASTER_KEY", "")
	SocialReset()

	if SocialAvailable() {
		t.Fatal("should be unavailable when no key exists")
	}
}

func TestSocialGenerateKeyIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		SocialReset()
	})
	os.Setenv("HOME", dir)

	key1, err := SocialGenerateKey()
	if err != nil {
		t.Fatalf("generate 1: %v", err)
	}
	SocialReset()
	key2, err := SocialGenerateKey()
	if err != nil {
		t.Fatalf("generate 2: %v", err)
	}
	if string(key1) != string(key2) {
		t.Fatal("second generate should return existing key, not regenerate")
	}
}
