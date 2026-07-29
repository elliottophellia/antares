package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/commands"
	"github.com/enowdev/antares/internal/version"
)

// jsonBody marshals v to a reader for an HTTP request body.
func jsonBody(v any) (io.Reader, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}

// This file registers the bot's commands with each platform so they appear as
// native, autocompleted commands rather than something the user must remember
// and type blind. The command list is the gateway-surface catalogue, the same
// commands that already work when typed.
//
// Discord: real slash commands (CHAT_INPUT). Registering makes them appear;
// clicking one sends an INTERACTION_CREATE the adapter answers (see discord.go).
// Telegram: setMyCommands, which fills the "/" menu. Telegram still delivers a
// chosen command as an ordinary message, so no interaction plumbing is needed.

// gatewayCommandSpecs returns the commands worth surfacing on a chat platform:
// the gateway-eligible catalogue, minus ones that need a screen.
func gatewayCommandSpecs() []commands.Spec {
	skip := map[string]bool{"export": true, "fork": true, "undo": true}
	var out []commands.Spec
	for _, s := range commands.Catalogue(commands.SurfaceGateway) {
		if skip[s.Name] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ---- Discord ----------------------------------------------------------------

type discordCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        int    `json:"type"` // 1 = CHAT_INPUT (slash)
}

func buildDiscordCommands(specs []commands.Spec) []discordCommand {
	out := make([]discordCommand, 0, len(specs))
	for _, s := range specs {
		desc := s.Summary
		if desc == "" {
			desc = s.Name
		}
		if len(desc) > 100 {
			desc = desc[:100] // Discord caps description at 100 chars
		}
		out = append(out, discordCommand{Name: s.Name, Description: desc, Type: 1})
	}
	return out
}

// discordAppIDDecode resolves the bot's application id (== its user id) from
// the token, so a server handler with no running adapter can still register.
func discordAppIDDecode(ctx context.Context, token string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordAPI+"/users/@me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/enowdev/antares, "+version.Version+")")
	resp, err := verifyClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot reach Discord: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Discord returned %d", resp.StatusCode)
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return "", err
	}
	return me.ID, nil
}

func discordCommandsPUT(ctx context.Context, token, appID string, body []discordCommand) error {
	if strings.TrimSpace(appID) == "" {
		resolved, err := discordAppIDDecode(ctx, token)
		if err != nil {
			return err
		}
		appID = resolved
	}
	// A bulk PUT is the whole set — an empty array clears every command, which
	// is exactly what "clear stale commands" needs.
	return discordJSON(ctx, token, http.MethodPut, "/applications/"+appID+"/commands", body)
}

// RegisterDiscordCommands publishes the gateway command set as global slash
// commands. Global commands can take up to an hour to propagate on Discord's
// side the first time; re-registering the same set is cheap and idempotent.
func RegisterDiscordCommands(ctx context.Context, token, appID string) error {
	return discordCommandsPUT(ctx, token, appID, buildDiscordCommands(gatewayCommandSpecs()))
}

// ClearDiscordCommands removes every registered global slash command.
func ClearDiscordCommands(ctx context.Context, token, appID string) error {
	return discordCommandsPUT(ctx, token, appID, []discordCommand{})
}

func discordJSON(ctx context.Context, token, method, path string, body any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	payload, err := jsonBody(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, discordAPI+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/enowdev/antares, "+version.Version+")")
	resp, err := verifyClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Discord: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Discord returned %d registering commands", resp.StatusCode)
	}
	return nil
}

// ---- Telegram ---------------------------------------------------------------

type telegramCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func buildTelegramCommands(specs []commands.Spec) []telegramCommand {
	out := make([]telegramCommand, 0, len(specs))
	for _, s := range specs {
		desc := s.Summary
		if desc == "" {
			desc = s.Name
		}
		if len(desc) > 256 {
			desc = desc[:256]
		}
		// Telegram command names are lowercase, 1-32 chars, letters/digits/_.
		name := strings.ToLower(s.Name)
		if len(name) > 32 {
			name = name[:32]
		}
		out = append(out, telegramCommand{Command: name, Description: desc})
	}
	return out
}

// SetTelegramCommands publishes the command menu via setMyCommands.
func SetTelegramCommands(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body := map[string]any{"commands": buildTelegramCommands(gatewayCommandSpecs())}
	return telegramCall(ctx, token, "setMyCommands", body)
}

// ClearTelegramCommands empties the command menu via deleteMyCommands.
func ClearTelegramCommands(ctx context.Context, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return telegramCall(ctx, token, "deleteMyCommands", map[string]any{})
}

func telegramCall(ctx context.Context, token, method string, body map[string]any) error {
	payload, err := jsonBody(body)
	if err != nil {
		return err
	}
	url := "https://api.telegram.org/bot" + strings.TrimSpace(token) + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := verifyClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Telegram: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Telegram returned %d on %s", resp.StatusCode, method)
	}
	return nil
}
