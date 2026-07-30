package secret

import "testing"

func TestBoxRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	b, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"", "hunter2", "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END-----"} {
		enc, err := b.Encrypt(plain)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		if plain != "" && enc == plain {
			t.Fatalf("ciphertext equals plaintext for %q", plain)
		}
		got, err := b.Decrypt(enc)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != plain {
			t.Fatalf("round trip: got %q want %q", got, plain)
		}
	}
}

func TestWrongKeyFails(t *testing.T) {
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	k2[0] = 1
	b1, _ := NewBox(k1)
	b2, _ := NewBox(k2)
	enc, _ := b1.Encrypt("secret")
	if _, err := b2.Decrypt(enc); err == nil {
		t.Fatal("decrypting with the wrong key should fail")
	}
}
