package gateway

import (
	"encoding/json"
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestParseSlackMessage(t *testing.T) {
	dm := json.RawMessage(`{"event":{"type":"message","user":"U123","text":"hello","channel":"D999","channel_type":"im","ts":"1.2"}}`)
	msg, ok := parseSlackMessage(dm, "UBOT")
	if !ok {
		t.Fatal("a plain DM should parse")
	}
	if msg.Platform != "slack" || msg.ChannelID != "D999" || msg.UserID != "U123" || msg.Text != "hello" || !msg.IsDirect {
		t.Fatalf("bad parse: %+v", msg)
	}
}

func TestParseSlackIgnoresNoise(t *testing.T) {
	cases := []string{
		`{"event":{"type":"message","subtype":"channel_join","user":"U1","text":"x","channel":"C1"}}`, // subtype
		`{"event":{"type":"message","bot_id":"B1","text":"x","channel":"C1"}}`,                        // a bot
		`{"event":{"type":"reaction_added","user":"U1","channel":"C1"}}`,                              // not a message
		`{"event":{"type":"message","user":"UBOT","text":"x","channel":"C1"}}`,                        // ourselves
		`{"event":{"type":"message","user":"U1","text":"   ","channel":"C1"}}`,                        // blank
	}
	for i, c := range cases {
		if _, ok := parseSlackMessage(json.RawMessage(c), "UBOT"); ok {
			t.Errorf("case %d should have been ignored: %s", i, c)
		}
	}
}

func TestAdaptersConstruct(t *testing.T) {
	if NewSlack(config.Slack{}, nil).Name() != "slack" {
		t.Fatal("slack name wrong")
	}
	m := NewMatrix(config.Matrix{Homeserver: "https://matrix.org/"}, nil)
	if m.Name() != "matrix" {
		t.Fatal("matrix name wrong")
	}
	if m.base != "https://matrix.org" {
		t.Fatalf("homeserver trailing slash not trimmed: %q", m.base)
	}
}
