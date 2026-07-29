package gateway

import (
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestResolveBindingSpecificity(t *testing.T) {
	bindings := []config.Binding{
		{ID: "p", Platform: "discord", Enabled: true, Role: "platform"},
		{ID: "g", Platform: "discord", GuildID: "G1", Enabled: true, Role: "guild"},
		{ID: "gc", Platform: "discord", GuildID: "G1", ChannelID: "C1", Enabled: true, Role: "guildchan"},
		{ID: "c", Platform: "discord", ChannelID: "C2", Enabled: true, Role: "chanonly"},
		{ID: "off", Platform: "discord", GuildID: "G1", ChannelID: "C9", Enabled: false, Role: "disabled"},
	}
	cases := []struct {
		guild, channel, wantRole string
	}{
		{"G1", "C1", "guildchan"}, // most specific
		{"G1", "C7", "guild"},     // guild match, channel wildcard
		{"G9", "C2", "chanonly"},  // channel-only match beats platform
		{"G9", "C9", "platform"},  // nothing specific → platform-wide
		{"G1", "C9", "guild"},     // disabled binding ignored; falls to guild
	}
	for _, tc := range cases {
		got := ResolveBinding(bindings, "discord", tc.guild, tc.channel)
		if got == nil {
			t.Errorf("guild=%s channel=%s: got nil, want %q", tc.guild, tc.channel, tc.wantRole)
			continue
		}
		if got.Role != tc.wantRole {
			t.Errorf("guild=%s channel=%s: got %q, want %q", tc.guild, tc.channel, got.Role, tc.wantRole)
		}
	}
}

func TestResolveBindingPlatformIsolation(t *testing.T) {
	bindings := []config.Binding{
		{ID: "d", Platform: "discord", ChannelID: "C1", Enabled: true, Role: "d"},
	}
	// A telegram message must not match a discord binding.
	if got := ResolveBinding(bindings, "telegram", "", "C1"); got != nil {
		t.Errorf("telegram matched a discord binding: %+v", got)
	}
	if !HasBindings(bindings, "discord") {
		t.Error("HasBindings(discord) = false, want true")
	}
	if HasBindings(bindings, "telegram") {
		t.Error("HasBindings(telegram) = true, want false")
	}
}

func TestBindingAllowsRoles(t *testing.T) {
	// DMs are never gated by roles.
	if !BindingAllowsRoles(&config.Binding{}, true, nil) {
		t.Error("a DM should never be role-gated")
	}
	// A server binding with no roles serves no one.
	if BindingAllowsRoles(&config.Binding{}, false, []string{"r1"}) {
		t.Error("empty AllowedRoles on a server binding must deny")
	}
	b := &config.Binding{AllowedRoles: []string{"mod", "dev"}}
	if !BindingAllowsRoles(b, false, []string{"member", "dev"}) {
		t.Error("sender holding 'dev' should be allowed")
	}
	if BindingAllowsRoles(b, false, []string{"member"}) {
		t.Error("sender without an allowed role should be denied")
	}
	if BindingAllowsRoles(b, false, nil) {
		t.Error("sender with no roles should be denied when roles are required")
	}
}

func TestBindingAllowsUser(t *testing.T) {
	if !BindingAllowsUser(nil, "u1") {
		t.Error("nil binding should allow anyone")
	}
	open := &config.Binding{}
	if !BindingAllowsUser(open, "u1") {
		t.Error("empty allow list should allow anyone")
	}
	restricted := &config.Binding{AllowedUsers: []string{"u1", "u2"}}
	if !BindingAllowsUser(restricted, "u2") {
		t.Error("u2 is on the list, should be allowed")
	}
	if BindingAllowsUser(restricted, "u3") {
		t.Error("u3 is not on the list, should be denied")
	}
}
