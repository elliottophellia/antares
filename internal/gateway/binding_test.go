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

func TestBindingAdmits(t *testing.T) {
	// No restriction at all → admit anyone.
	if !BindingAdmits(&config.Binding{}, false, "u1", nil) {
		t.Error("a binding with no user/role limits should admit anyone")
	}
	// User allow list only.
	users := &config.Binding{AllowedUsers: []string{"u1", "u2"}}
	if !BindingAdmits(users, false, "u2", nil) {
		t.Error("u2 is listed, should be admitted")
	}
	if BindingAdmits(users, false, "u3", []string{"anyrole"}) {
		t.Error("u3 not listed and no role rule → deny")
	}
	// User + role: an explicit user match wins even with roles set, and a role
	// match also admits. Crucially, a listed user with NO matching role is still
	// admitted (users OR roles, not AND).
	both := &config.Binding{AllowedUsers: []string{"u1"}, AllowedRoles: []string{"mod"}}
	if !BindingAdmits(both, false, "u1", nil) {
		t.Error("listed user must be admitted regardless of roles")
	}
	if !BindingAdmits(both, false, "u9", []string{"mod"}) {
		t.Error("unlisted user holding an allowed role should be admitted")
	}
	if BindingAdmits(both, false, "u9", []string{"member"}) {
		t.Error("unlisted user without an allowed role should be denied")
	}
	// Roles are ignored in a DM: a role-only binding admits the DM sender.
	roleOnly := &config.Binding{AllowedRoles: []string{"mod"}}
	if !BindingAdmits(roleOnly, true, "u1", nil) {
		t.Error("a DM should not be role-gated")
	}
	// In a server, a role-only binding denies someone without the role.
	if BindingAdmits(roleOnly, false, "u1", []string{"member"}) {
		t.Error("server sender without the allowed role should be denied")
	}
}
