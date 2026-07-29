package gateway

import (
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// ResolveBinding picks the most specific enabled binding for a message on a
// platform, given its guild (server) and channel ids. Specificity order:
//
//	guild + channel  >  channel only  >  guild only  >  platform only
//
// It returns nil when no binding matches. A nil result from a platform that has
// NO bindings at all means "use the default agent"; a nil result from a
// platform that DOES have bindings means "ignore this channel" — the caller
// distinguishes the two with HasBindings.
func ResolveBinding(bindings []config.Binding, platform, guildID, channelID string) *config.Binding {
	platform = strings.ToLower(strings.TrimSpace(platform))
	var best *config.Binding
	bestScore := -1
	for i := range bindings {
		b := &bindings[i]
		if !b.Enabled || !strings.EqualFold(strings.TrimSpace(b.Platform), platform) {
			continue
		}
		// A set field must match; an empty field is a wildcard.
		if g := strings.TrimSpace(b.GuildID); g != "" && g != guildID {
			continue
		}
		if c := strings.TrimSpace(b.ChannelID); c != "" && c != channelID {
			continue
		}
		// Score by how many scope fields are pinned — more specific wins.
		score := 0
		if strings.TrimSpace(b.GuildID) != "" {
			score += 2
		}
		if strings.TrimSpace(b.ChannelID) != "" {
			score += 1
		}
		if score > bestScore {
			best, bestScore = b, score
		}
	}
	return best
}

// HasBindings reports whether a platform has any binding configured at all
// (enabled or not). When true and ResolveBinding returns nil for a GROUP
// channel, the channel is unregistered and the bot should stay silent. Direct
// messages are never gated this way — the caller lets DMs through regardless.
func HasBindings(bindings []config.Binding, platform string) bool {
	platform = strings.ToLower(strings.TrimSpace(platform))
	for i := range bindings {
		if strings.EqualFold(strings.TrimSpace(bindings[i].Platform), platform) {
			return true
		}
	}
	return false
}

// BindingAllowsUser reports whether a binding's per-scope allow list admits a
// user. An empty list admits anyone (subject to the platform's own pairing).
func BindingAllowsUser(b *config.Binding, userID string) bool {
	if b == nil || len(b.AllowedUsers) == 0 {
		return true
	}
	return contains(b.AllowedUsers, userID)
}

// BindingAllowsRoles gates a Discord server binding by the sender's server
// roles. It applies only to guild (non-direct) messages. The sender must hold
// at least one of the binding's AllowedRoles. An EMPTY AllowedRoles denies
// everyone — a server binding with no roles chosen serves no one (by design).
// Direct messages are never gated by roles (they have none).
func BindingAllowsRoles(b *config.Binding, isDirect bool, roles []string) bool {
	if b == nil || isDirect {
		return true
	}
	if len(b.AllowedRoles) == 0 {
		return false
	}
	for _, r := range roles {
		if contains(b.AllowedRoles, r) {
			return true
		}
	}
	return false
}
