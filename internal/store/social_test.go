package store

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/secret"
)

func newSocialTestStore(t *testing.T) *sqlStore {
	t.Helper()
	// Generate a social master key in a temp home.
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origEnv := os.Getenv("ANTARES_MASTER_KEY")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		os.Setenv("ANTARES_MASTER_KEY", origEnv)
		secret.SocialReset()
	})
	os.Setenv("HOME", dir)
	os.Setenv("ANTARES_MASTER_KEY", "")
	if _, err := secret.SocialGenerateKey(); err != nil {
		t.Fatalf("generate social key: %v", err)
	}
	secret.SocialReset()

	s := newTestStore(t).(*sqlStore)
	// Reset the lazy cache so it picks up the test key.
	s.socialBoxOnce = sync.Once{}
	return s
}

func TestSocialAccountCRUD(t *testing.T) {
	s := newSocialTestStore(t)
	ctx := context.Background()

	now := time.Now()
	a := &SocialAccount{
		ID:            "acct_1",
		Platform:      "instagram",
		DisplayName:   "Test User",
		Username:      "testuser",
		Password:      "supersecretpass",
		RecoveryCodes: "code1\ncode2",
		ProfileURL:    "https://instagram.com/testuser",
		Status:        "connected",
		RAGNamespace:  "social/instagram",
		SkillName:     "social-instagram",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.PutSocialAccount(ctx, a); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.GetSocialAccount(ctx, "acct_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Username != "testuser" {
		t.Fatalf("username = %q, want testuser", got.Username)
	}
	if got.Password != "supersecretpass" {
		t.Fatalf("password not decrypted: %q", got.Password)
	}
	if got.RecoveryCodes != "code1\ncode2" {
		t.Fatalf("recovery not decrypted: %q", got.RecoveryCodes)
	}

	list, err := s.ListSocialAccounts(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	if err := s.DeleteSocialAccount(ctx, "acct_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetSocialAccount(ctx, "acct_1"); err == nil {
		t.Fatal("should be deleted")
	}
}

func TestSocialAccountUpdatePreservesEncryption(t *testing.T) {
	s := newSocialTestStore(t)
	ctx := context.Background()

	a := &SocialAccount{
		ID:        "acct_2",
		Platform:  "x",
		Username:  "user1",
		Password:  "pass1",
		Status:    "connected",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.PutSocialAccount(ctx, a); err != nil {
		t.Fatalf("put 1: %v", err)
	}

	a.DisplayName = "Updated Name"
	a.Password = "pass2"
	a.UpdatedAt = time.Now()
	if err := s.PutSocialAccount(ctx, a); err != nil {
		t.Fatalf("put 2: %v", err)
	}

	got, err := s.GetSocialAccount(ctx, "acct_2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Password != "pass2" {
		t.Fatalf("password = %q, want pass2", got.Password)
	}
	if got.DisplayName != "Updated Name" {
		t.Fatalf("display_name = %q, want Updated Name", got.DisplayName)
	}
}

func TestSocialAccountStoreFailsWithoutKey(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origEnv := os.Getenv("ANTARES_MASTER_KEY")
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		os.Setenv("ANTARES_MASTER_KEY", origEnv)
		secret.SocialReset()
	})
	os.Setenv("HOME", dir)
	os.Setenv("ANTARES_MASTER_KEY", "")
	secret.SocialReset()

	s := newTestStore(t).(*sqlStore)
	s.socialBoxOnce = sync.Once{}

	a := &SocialAccount{
		ID:        "acct_3",
		Platform:  "facebook",
		Username:  "nouser",
		Password:  "shouldfail",
		Status:    "not_created",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.PutSocialAccount(context.Background(), a); err == nil {
		t.Fatal("should fail without social key")
	}
}
