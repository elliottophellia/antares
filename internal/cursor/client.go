package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("cursor api error: %d", e.Status)
}

func New(o Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if base == "" {
		base = "https://api.cursor.com"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid Cursor base URL")
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: base, apiKey: strings.TrimSpace(o.APIKey), http: hc}, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return c.decodeAPIError(resp)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.doJSON(ctx, http.MethodGet, "/v1/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Models(ctx context.Context) (*ModelCatalog, error) {
	var out ModelCatalog
	if err := c.doJSON(ctx, http.MethodGet, "/v1/models", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) decodeAPIError(resp *http.Response) error {
	const maxBody = 64 << 10
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}

	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	apiErr := &APIError{Status: resp.StatusCode}
	if json.Unmarshal(raw, &payload) == nil {
		if payload.Error.Message != "" || payload.Error.Code != "" {
			apiErr.Code = payload.Error.Code
			apiErr.Message = payload.Error.Message
		} else {
			apiErr.Code = payload.Code
			apiErr.Message = payload.Message
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = "request failed"
	}

	if c.apiKey != "" {
		apiErr.Code = strings.ReplaceAll(apiErr.Code, c.apiKey, "[REDACTED]")
		apiErr.Message = strings.ReplaceAll(apiErr.Message, c.apiKey, "[REDACTED]")
	}
	if len(apiErr.Message) > 240 {
		apiErr.Message = apiErr.Message[:240]
	}

	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			apiErr.RetryAfter = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			d := time.Until(t)
			if d > 0 {
				apiErr.RetryAfter = d
			}
		}
	}

	return apiErr
}

func IsAuthError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden)
}

func IsRateLimit(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests
}

func IsStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}
