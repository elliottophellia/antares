package gateway

import "testing"

func TestDiscordPayloadPlainNoReply(t *testing.T) {
	p := discordMessagePayload("hello", "")
	if p["content"] != "hello" {
		t.Fatalf("content = %v", p["content"])
	}
	if _, ok := p["message_reference"]; ok {
		t.Fatal("no reply requested, but message_reference was set")
	}
	am, _ := p["allowed_mentions"].(map[string]any)
	if am == nil {
		t.Fatal("allowed_mentions missing")
	}
	if _, ok := am["replied_user"]; ok {
		t.Fatal("replied_user must be absent when not replying")
	}
}

func TestDiscordPayloadReplyReferencesAndPings(t *testing.T) {
	p := discordMessagePayload("hi", "msg-123")
	ref, ok := p["message_reference"].(map[string]any)
	if !ok {
		t.Fatal("reply requested but message_reference missing")
	}
	if ref["message_id"] != "msg-123" {
		t.Fatalf("message_id = %v, want msg-123", ref["message_id"])
	}
	if ref["fail_if_not_exists"] != false {
		t.Fatal("fail_if_not_exists must be false so a deleted trigger does not error the send")
	}
	am := p["allowed_mentions"].(map[string]any)
	if am["replied_user"] != true {
		t.Fatal("replying must ping the replied-to author (replied_user=true)")
	}
}

// Embed sends pass empty content; the key must then be omitted (Discord rejects
// an empty content string alongside embeds).
func TestDiscordPayloadOmitsEmptyContent(t *testing.T) {
	p := discordMessagePayload("", "msg-1")
	if _, ok := p["content"]; ok {
		t.Fatal("empty content must be omitted, not sent as \"\"")
	}
}
