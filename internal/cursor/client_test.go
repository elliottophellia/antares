package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMeAndModelsUseBearerAndDecodeCatalog(t *testing.T) {
	const key = "synthetic-cursor-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/v1/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiKeyName": "test key", "createdAt": "2026-08-12T00:00:00Z",
			})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{
				map[string]any{
					"id": "composer-2", "displayName": "Composer 2",
					"parameters": []any{map[string]any{
						"id": "fast", "values": []any{map[string]any{"value": "true"}},
					}},
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	me, err := client.Me(context.Background())
	if err != nil || me.APIKeyName != "test key" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	models, err := client.Models(context.Background())
	if err != nil || len(models.Items) != 1 || models.Items[0].ID != "composer-2" {
		t.Fatalf("Models = %+v, %v", models, err)
	}
}

func TestAPIErrorNeverLeaksAPIKey(t *testing.T) {
	const key = "synthetic-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"rejected synthetic-secret"}}`)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	_, err := client.Me(context.Background())
	if err == nil || !IsAuthError(err) {
		t.Fatalf("expected auth error, got %v", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaked key: %v", err)
	}
}

func TestAPIErrorClassificationAndRetryAfter(t *testing.T) {
	for _, status := range []int{400, 404, 409, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":"synthetic","message":"request failed"}`)
			}))
			defer srv.Close()
			client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
			_, err := client.Me(context.Background())
			if !IsStatus(err, status) {
				t.Fatalf("status %d classified as %v", status, err)
			}
			if status == 429 {
				if !IsRateLimit(err) {
					t.Fatalf("429 not classified as rate limit: %v", err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.RetryAfter != 7*time.Second {
					t.Fatalf("RetryAfter = %v, want 7s", apiErr)
				}
			}
		})
	}
}

func TestAPIErrorMessageTruncatedOnRuneBoundary(t *testing.T) {
	longMsg := strings.Repeat("a", 239) + "é" + strings.Repeat("é", 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		body, _ := json.Marshal(map[string]string{"message": longMsg})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	_, err := client.Me(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !utf8.ValidString(msg) {
		t.Fatalf("invalid UTF-8: %q", msg)
	}
	if got := utf8.RuneCountInString(msg); got != 240 {
		t.Fatalf("rune count = %d, want 240", got)
	}
	want := strings.Repeat("a", 239) + "é"
	if msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}
