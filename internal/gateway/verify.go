package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/version"
)

// Identity is what a platform reports about the bot behind a token.
type Identity struct {
	Name   string `json:"name"`
	Handle string `json:"handle"`
}

// VerifyToken asks the platform whether a bot token is real before Antares
// stores it. Saving an unverified token means the failure surfaces later, in a
// background reconnect loop, where nobody is watching.
func VerifyToken(ctx context.Context, platform, token string) (*Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("a bot token is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch strings.ToLower(platform) {
	case "telegram":
		return verifyTelegram(ctx, token)
	case "discord":
		return verifyDiscord(ctx, token)
	default:
		return nil, fmt.Errorf("unknown platform %q", platform)
	}
}

var verifyClient = &http.Client{Timeout: 15 * time.Second}

func verifyTelegram(ctx context.Context, token string) (*Identity, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.telegram.org/bot"+token+"/getMe", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := verifyClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Telegram: %w", err)
	}
	defer resp.Body.Close()

	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("unexpected reply from Telegram")
	}
	if !envelope.OK {
		if envelope.Description != "" {
			return nil, fmt.Errorf("%s", envelope.Description)
		}
		return nil, fmt.Errorf("Telegram rejected this token")
	}
	return &Identity{
		Name:   envelope.Result.FirstName,
		Handle: "@" + envelope.Result.Username,
	}, nil
}

func verifyDiscord(ctx context.Context, token string) (*Identity, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", discordAPI+"/users/@me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/enowdev/antares, "+version.Version+")")

	resp, err := verifyClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("Discord rejected this token")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Discord returned %d", resp.StatusCode)
	}

	var user struct {
		Username      string `json:"username"`
		GlobalName    string `json:"global_name"`
		Discriminator string `json:"discriminator"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("unexpected reply from Discord")
	}
	name := user.GlobalName
	if name == "" {
		name = user.Username
	}
	return &Identity{Name: name, Handle: "@" + user.Username}, nil
}
