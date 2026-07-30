package config

import (
	"os"
	"strings"
)

// SoulPath is where the agent's identity lives — a Markdown file the user (and
// the agent itself, during its first conversation) can shape. It is global: one
// soul across web, TUI, and every gateway.
func SoulPath() string { return Path("SOUL.md") }

// defaultSoul is the identity a fresh install starts with: deliberately blank of
// personality, with a marker line the agent watches for. While this marker is
// present the agent knows it has not been given an identity yet and should ask
// for one at the start of the first real conversation.
const soulUnsetMarker = "<!-- antares:soul-unset -->"

const defaultSoul = soulUnsetMarker + `
# (unnamed agent)

I don't have an identity yet — no name, no personality, no idea who I'm working
with. The first thing I should do in my first conversation is find out: who the
user is, what they'd like to call me, how they want me to talk, and any quirks or
principles they'd like me to have. Then I write it all here.
`

// Soul returns the current soul text, creating the default file if none exists.
func Soul() string {
	b, err := os.ReadFile(SoulPath())
	if err != nil {
		return defaultSoul
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return defaultSoul
	}
	return s
}

// SoulIsUnset reports whether the agent still lacks an identity — the default is
// in place (marker present) or the file is missing/empty. This is what triggers
// the first-conversation identity interview.
func SoulIsUnset() bool {
	b, err := os.ReadFile(SoulPath())
	if err != nil {
		return true
	}
	s := strings.TrimSpace(string(b))
	return s == "" || strings.Contains(s, soulUnsetMarker)
}

// SaveSoul writes the soul file (creating the home dir if needed). Passing empty
// content resets it to the unset default so the interview can run again.
func SaveSoul(content string) error {
	if err := EnsureHome(); err != nil {
		return err
	}
	body := strings.TrimSpace(content)
	if body == "" {
		body = defaultSoul
	}
	return os.WriteFile(SoulPath(), []byte(body+"\n"), 0o600)
}
