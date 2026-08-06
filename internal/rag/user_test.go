package rag

import "testing"

func TestUserCollectionIsStableAndScoped(t *testing.T) {
	a := UserCollection("discord", "12345")
	if a == "" {
		t.Fatal("a real user id must yield a collection")
	}
	// Stable across calls.
	if a != UserCollection("discord", "12345") {
		t.Fatal("same platform+user must give the same collection")
	}
	// Different user → different collection.
	if a == UserCollection("discord", "67890") {
		t.Fatal("different users must not share a collection")
	}
	// Same id on a different platform → different collection (ids are not global).
	if a == UserCollection("telegram", "12345") {
		t.Fatal("same id on another platform must not collide")
	}
	// Namespaced so it never collides with project/conversation collections.
	if a[:5] != "user-" {
		t.Fatalf("collection %q must start with user-", a)
	}
}

func TestUserCollectionEmptyWhenNoUser(t *testing.T) {
	if UserCollection("discord", "") != "" {
		t.Fatal("no user id must yield no collection so callers can skip")
	}
	if UserCollection("discord", "   ") != "" {
		t.Fatal("blank user id must yield no collection")
	}
}
