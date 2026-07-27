package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// Speak synthesises speech from text via the OpenAI-compatible /audio/speech
// endpoint, returning the audio bytes and their file extension.
func (c *openAIClient) Speak(ctx context.Context, model, voice, format, text string) ([]byte, string, error) {
	if model == "" {
		model = "tts-1"
	}
	if voice == "" {
		voice = "alloy"
	}
	if format == "" {
		format = "mp3"
	}
	body := map[string]any{"model": model, "voice": voice, "input": text, "response_format": format}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.opts.BaseURL+"/audio/speech", bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	client := c.opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", describeTransport(err, c.opts.BaseURL)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &apiError{Status: resp.StatusCode, Body: string(data), URL: c.opts.BaseURL}
	}
	return data, format, nil
}

// Transcribe turns speech audio into text via /audio/transcriptions.
func (c *openAIClient) Transcribe(ctx context.Context, model, filename string, audio []byte) (string, error) {
	if model == "" {
		model = "whisper-1"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if filename == "" {
		filename = "audio.mp3"
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	_ = w.WriteField("model", model)
	_ = w.WriteField("response_format", "json")
	_ = w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", c.opts.BaseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	client := c.opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", describeTransport(err, c.opts.BaseURL)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &apiError{Status: resp.StatusCode, Body: string(data), URL: c.opts.BaseURL}
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode transcription: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}
