package gateway

import (
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestGatewaySessionKeyPerUserInGroups(t *testing.T) {
	cfg := &config.Config{GroupSessionsPerUser: true}

	group := InboundMessage{Platform: "discord", ChannelID: "chan1", UserID: "alice", IsDirect: false}
	group2 := InboundMessage{Platform: "discord", ChannelID: "chan1", UserID: "bob", IsDirect: false}

	ka := GatewaySessionKey(cfg, group)
	kb := GatewaySessionKey(cfg, group2)
	if ka == kb {
		t.Fatalf("two users in the same channel must get different session keys, both were %q", ka)
	}
	if ka != "gateway_session:discord:chan1:alice" {
		t.Fatalf("unexpected key %q", ka)
	}
}

func TestGatewaySessionKeyDMIsPerChannel(t *testing.T) {
	cfg := &config.Config{GroupSessionsPerUser: true}
	dm := InboundMessage{Platform: "telegram", ChannelID: "dm42", UserID: "alice", IsDirect: true}
	// A DM is already 1:1, so the user id must NOT be folded in — otherwise the
	// key would differ from the pre-existing per-channel one and orphan history.
	if got := GatewaySessionKey(cfg, dm); got != "gateway_session:telegram:dm42" {
		t.Fatalf("DM key = %q, want per-channel", got)
	}
}

func TestGatewaySessionKeySharedWhenFlagOff(t *testing.T) {
	cfg := &config.Config{GroupSessionsPerUser: false}
	a := InboundMessage{Platform: "discord", ChannelID: "chan1", UserID: "alice"}
	b := InboundMessage{Platform: "discord", ChannelID: "chan1", UserID: "bob"}
	if GatewaySessionKey(cfg, a) != GatewaySessionKey(cfg, b) {
		t.Fatal("with group_sessions_per_user off, a channel shares one session")
	}
}

// A missing user id (some platforms omit it) must not produce a dangling
// trailing colon; fall back to the per-channel key.
func TestGatewaySessionKeyNoUserFallsBack(t *testing.T) {
	cfg := &config.Config{GroupSessionsPerUser: true}
	m := InboundMessage{Platform: "discord", ChannelID: "chan1", UserID: "", IsDirect: false}
	if got := GatewaySessionKey(cfg, m); got != "gateway_session:discord:chan1" {
		t.Fatalf("empty user should fall back to per-channel, got %q", got)
	}
}
