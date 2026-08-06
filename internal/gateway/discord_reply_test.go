package gateway

import (
	"testing"

	"github.com/enowdev/antares/internal/config"
)

const botID = "bot-self-1"

// repliedTo builds the anonymous referenced-message struct dcMessage uses.
func repliedTo(authorID string) *struct {
	Author dcAuthor `json:"author"`
} {
	return &struct {
		Author dcAuthor `json:"author"`
	}{Author: dcAuthor{ID: authorID}}
}

func TestMessageAddressesBot(t *testing.T) {
	cases := []struct {
		name string
		msg  dcMessage
		text string
		want bool
	}{
		{
			name: "parsed mention",
			msg:  dcMessage{Mentions: []dcAuthor{{ID: botID}}},
			text: "<@" + botID + "> hi",
			want: true,
		},
		{
			name: "raw mention token only (mentions array lagging)",
			msg:  dcMessage{},
			text: "hey <@" + botID + "> what's up",
			want: true,
		},
		{
			name: "nickname mention token",
			msg:  dcMessage{},
			text: "<@!" + botID + "> yo",
			want: true,
		},
		{
			name: "reply to the bot",
			msg:  dcMessage{ReferencedMessage: repliedTo(botID)},
			text: "and another thing",
			want: true,
		},
		{
			name: "mention of someone else",
			msg:  dcMessage{Mentions: []dcAuthor{{ID: "someone-else"}}},
			text: "<@someone-else> look at this",
			want: false,
		},
		{
			name: "plain chatter",
			msg:  dcMessage{},
			text: "just talking in the channel",
			want: false,
		},
		{
			name: "reply to a different user",
			msg:  dcMessage{ReferencedMessage: repliedTo("other")},
			text: "nice",
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := messageAddressesBot(c.msg, c.text, botID); got != c.want {
				t.Fatalf("messageAddressesBot = %v, want %v", got, c.want)
			}
		})
	}
}

// The reply-mode gate is: reply when addressed OR when an enabled binding for
// this channel sets reply_mode "always". This is the exact condition
// handleMessage uses to decide whether to drop an un-addressed guild message.
func replyGate(bindings []config.Binding, guildID, channelID string, addressed bool) bool {
	b := ResolveBinding(bindings, "discord", guildID, channelID)
	always := b != nil && b.ReplyMode == "always"
	return addressed || always
}

func TestReplyModeAlwaysAnswersUnaddressed(t *testing.T) {
	const guild, channel = "g1", "c1"
	bindings := []config.Binding{
		{
			ID: "b1", Platform: "discord", GuildID: guild, ChannelID: channel,
			Enabled: true, ReplyMode: "always",
		},
	}
	// Un-addressed message in the always channel: must be answered.
	if !replyGate(bindings, guild, channel, false) {
		t.Fatal("reply_mode=always must answer an un-addressed message")
	}
	// Addressed message: answered regardless.
	if !replyGate(bindings, guild, channel, true) {
		t.Fatal("an addressed message must always be answered")
	}
}

func TestReplyModeMentionIgnoresUnaddressed(t *testing.T) {
	const guild, channel = "g1", "c1"
	bindings := []config.Binding{
		{
			ID: "b1", Platform: "discord", GuildID: guild, ChannelID: channel,
			Enabled: true, ReplyMode: "mention",
		},
	}
	if replyGate(bindings, guild, channel, false) {
		t.Fatal("reply_mode=mention must ignore an un-addressed message")
	}
	if !replyGate(bindings, guild, channel, true) {
		t.Fatal("mention mode must still answer when addressed")
	}
}

// A disabled binding is invisible to ResolveBinding, so its reply_mode never
// takes effect — the channel falls back to addressed-only. This is the most
// likely reason "always" appears set in the UI but does nothing.
func TestReplyModeAlwaysIgnoredWhenBindingDisabled(t *testing.T) {
	const guild, channel = "g1", "c1"
	bindings := []config.Binding{
		{
			ID: "b1", Platform: "discord", GuildID: guild, ChannelID: channel,
			Enabled: false, ReplyMode: "always",
		},
	}
	if replyGate(bindings, guild, channel, false) {
		t.Fatal("a disabled always-binding must not answer un-addressed messages")
	}
}

// A more specific binding (guild+channel) wins over a broad one, so its
// reply_mode is the one that applies.
func TestReplyModeMostSpecificBindingWins(t *testing.T) {
	const guild, channel = "g1", "c1"
	bindings := []config.Binding{
		{ID: "guild-wide", Platform: "discord", GuildID: guild, Enabled: true, ReplyMode: "mention"},
		{ID: "this-channel", Platform: "discord", GuildID: guild, ChannelID: channel, Enabled: true, ReplyMode: "always"},
	}
	if !replyGate(bindings, guild, channel, false) {
		t.Fatal("the channel-specific always binding should win over the guild-wide mention binding")
	}
}

func TestDiscordDisplayName(t *testing.T) {
	// Server nickname wins over everything.
	if got := discordDisplayName("Nicky", "GlobalGuy", "user123"); got != "Nicky" {
		t.Fatalf("nick should win, got %q", got)
	}
	// No nick → account display name.
	if got := discordDisplayName("", "GlobalGuy", "user123"); got != "GlobalGuy" {
		t.Fatalf("global_name should win over username, got %q", got)
	}
	// Neither → username.
	if got := discordDisplayName("", "", "user123"); got != "user123" {
		t.Fatalf("username fallback, got %q", got)
	}
	// Whitespace-only fields are skipped.
	if got := discordDisplayName("  ", "  ", "user123"); got != "user123" {
		t.Fatalf("blank fields must be skipped, got %q", got)
	}
	// All empty → empty.
	if got := discordDisplayName("", "", ""); got != "" {
		t.Fatalf("all-empty should be empty, got %q", got)
	}
}
