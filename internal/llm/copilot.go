package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GitHub Copilot is an OpenAI-compatible endpoint, but its auth is two-legged:
// a long-lived GitHub OAuth token (obtained once via the device flow) is
// exchanged for a short-lived Copilot token that the chat endpoint accepts. The
// copilot vendor of the OpenAI adapter refreshes that token transparently and
// adds the editor headers the API requires.

// copilotClientID is the public client id GitHub's editor integrations use for
// the device-authorization flow.
const copilotClientID = "Iv1.b507a08c87ecfe98"

// copilotTokenSource exchanges a GitHub OAuth token for Copilot tokens, caching
// each until shortly before it expires.
type copilotTokenSource struct {
	ghToken string
	client  *http.Client

	mu      sync.Mutex
	cached  string
	expires time.Time
}

func (c *copilotTokenSource) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != "" && time.Now().Before(c.expires.Add(-1*time.Minute)) {
		return c.cached, nil
	}
	if c.ghToken == "" {
		return "", errors.New("copilot needs a GitHub token: run `antares auth copilot`, then set it as the provider api_key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/copilot_internal/v2/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+c.ghToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.11.1")

	client := c.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("copilot token exchange failed (HTTP %d) — the GitHub token may lack Copilot access", resp.StatusCode)
	}
	c.cached = out.Token
	if out.ExpiresAt > 0 {
		c.expires = time.Unix(out.ExpiresAt, 0)
	} else {
		c.expires = time.Now().Add(25 * time.Minute)
	}
	return c.cached, nil
}

// copilotHeaders are the editor headers the Copilot API expects, in addition to
// the bearer token.
func copilotHeaders(token string) map[string]string {
	return map[string]string{
		"Authorization":          "Bearer " + token,
		"Editor-Version":         "vscode/1.95.0",
		"Editor-Plugin-Version":  "copilot-chat/0.11.1",
		"Copilot-Integration-Id": "vscode-chat",
		"User-Agent":             "GitHubCopilotChat/0.11.1",
	}
}

// ---- device-flow login ------------------------------------------------------

// DeviceCode is the first step of the GitHub device flow: show the user the
// code and URL, then poll with Verificaton until they authorise.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// StartCopilotLogin begins the device flow and returns the code to show the user.
func StartCopilotLogin(ctx context.Context) (*DeviceCode, error) {
	form := url.Values{"client_id": {copilotClientID}, "scope": {"read:user"}}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, err
	}
	if dc.DeviceCode == "" {
		return nil, errors.New("github did not return a device code")
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

// PollCopilotToken polls until the user authorises and returns the GitHub OAuth
// token. It blocks up to the device code's lifetime.
func PollCopilotToken(ctx context.Context, dc *DeviceCode) (string, error) {
	deadline := time.Now().Add(time.Duration(maxInt(dc.ExpiresIn, 300)) * time.Second)
	interval := time.Duration(dc.Interval) * time.Second
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		form := url.Values{
			"client_id":   {copilotClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		req, err := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		var out struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if out.AccessToken != "" {
			return out.AccessToken, nil
		}
		switch out.Error {
		case "authorization_pending", "slow_down", "":
			// keep waiting
		default:
			return "", fmt.Errorf("device authorisation failed: %s", out.Error)
		}
	}
	return "", errors.New("timed out waiting for GitHub authorisation")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
