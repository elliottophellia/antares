package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The bodies below are SSE recordings. A truncated one ends where a gateway
// that lost its upstream would close the connection: cleanly, mid-answer, with
// no terminal marker. End of body is not end of answer, and an adapter that
// treats the two as the same reports a cut-off turn as a finished one.

// sseFramingServer serves one recorded body and closes the connection after it,
// which is what the adapter sees when a stream is cut short.
func sseFramingServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func openAIFramingClient(t *testing.T, body string) *openAIClient {
	srv := sseFramingServer(t, body)
	return &openAIClient{opts: Options{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}, vendor: "compat"}
}

func anthropicFramingClient(t *testing.T, body string) *anthropicClient {
	srv := sseFramingServer(t, body)
	return &anthropicClient{opts: Options{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}}
}

func TestStreamFramingOpenAICutStreamsAreRetryableErrors(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{
			// The brief's case: arguments cut mid-JSON, no [DONE].
			"arguments cut mid-json",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write_file","arguments":""}}]}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": \"/tm"}}]}}]}` + "\n\n",
		},
		{
			// The name arrived and nothing else did. Fabricating {} here is what
			// turns a cut stream into write_file with no path.
			"tool name with no arguments at all",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write_file","arguments":""}}]}}]}` + "\n\n",
		},
		{
			// Every argument payload that arrived is valid JSON, so nothing
			// downstream can tell this turn was cut. Only the missing terminal can.
			"cut between parallel tool calls",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"b\"}"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"id":"c3","type":"function","function":{"name":"read_file","arguments":""}}]}}]}` + "\n\n",
		},
		{
			"prose cut mid-sentence",
			`data: {"choices":[{"delta":{"content":"I will now "}}]}` + "\n\n",
		},
		{
			"empty body",
			"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := openAIFramingClient(t, c.body)
			resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
			if err == nil {
				t.Fatalf("a body that ended without [DONE] or a finish_reason was accepted as complete: %+v", resp)
			}
			if !Retryable(err) {
				t.Fatalf("a cut stream must reach the turn-level retry, got %v", err)
			}
			assertNoPartialAnswer(t, resp)
		})
	}
}

// Requiring both [DONE] and a finish_reason would break servers that send only
// one, so either has to be enough.
func TestStreamFramingOpenAIAcceptsEitherTerminalSignal(t *testing.T) {
	call := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}` + "\n\n"
	cases := []struct {
		name, body string
	}{
		{"both", call + `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" + "data: [DONE]\n\n"},
		{"finish_reason only", call + `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"},
		{"done only", call + "data: [DONE]\n\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := openAIFramingClient(t, c.body)
			resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
			if err != nil {
				t.Fatalf("a terminated stream must succeed: %v", err)
			}
			if len(resp.ToolCalls) != 1 {
				t.Fatalf("tool calls = %+v, want the one that was sent", resp.ToolCalls)
			}
			if got, want := resp.ToolCalls[0].Arguments, `{"path":"a"}`; got != want {
				t.Fatalf("arguments = %q, want %q", got, want)
			}
		})
	}
}

func TestStreamFramingAnthropicCutStreamsAreRetryableErrors(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{
			// The brief's case: the block that names the tool arrives and the
			// body ends. No content_block_stop, so the input never finished.
			"tool_use block then nothing",
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"model":"claude","usage":{"input_tokens":9}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write_file","input":{}}}` + "\n\n",
		},
		{
			"input cut mid-json",
			"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write_file","input":{}}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\": \"/tm"}}` + "\n\n",
		},
		{
			// A stop_reason is not a terminal marker: Anthropic still owes a
			// message_stop, and a gateway can drop the body in between.
			"message_delta without message_stop",
			"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := anthropicFramingClient(t, c.body)
			resp, err := client.Stream(context.Background(), Request{Model: "claude"}, func(Event) error { return nil })
			if err == nil {
				t.Fatalf("a body that ended without message_stop was accepted as complete: %+v", resp)
			}
			if !Retryable(err) {
				t.Fatalf("a cut stream must reach the turn-level retry, got %v", err)
			}
			assertNoPartialAnswer(t, resp)
		})
	}
}

func TestStreamFramingAnthropicCompleteStreamYieldsTheCall(t *testing.T) {
	body := "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude","usage":{"input_tokens":9}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"a\"}"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	client := anthropicFramingClient(t, body)
	resp, err := client.Stream(context.Background(), Request{Model: "claude"}, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("a terminated stream must succeed: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want the one that was sent", resp.ToolCalls)
	}
	if got, want := resp.ToolCalls[0].Arguments, `{"path":"a"}`; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}

// A tool that takes no parameters does not arrive as {} on the wire. Anthropic
// documents that such a call emits content_block_start and content_block_stop
// with no input_json_delta between them, so the accumulated input is empty;
// OpenAI opens the call with an empty arguments string. Both are complete
// answers, and the marker that says so is the provider's own close of the call
// — which is what tells them apart from a stream cut before the arguments came.
// Rejecting an empty argument payload on its own would break every one of them.
func TestStreamFramingParameterlessToolCallSurvives(t *testing.T) {
	anthropicBodies := map[string]string{
		// The documented shape: nothing at all between start and stop.
		"no input_json_delta at all": "",
		// Some servers open the run with an empty fragment before the real
		// ones; for a parameterless call that fragment is all there is.
		"empty partial_json": "event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}` + "\n\n",
	}

	for name, deltas := range anthropicBodies {
		t.Run("anthropic/"+name, func(t *testing.T) {
			body := "event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"snooze","input":{}}}` + "\n\n" +
				deltas +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n"

			client := anthropicFramingClient(t, body)
			resp, err := client.Stream(context.Background(), Request{Model: "claude"}, func(Event) error { return nil })
			if err != nil {
				t.Fatalf("a closed tool_use block with no input is a complete call: %v", err)
			}
			if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "snooze" {
				t.Fatalf("tool calls = %+v, want snooze", resp.ToolCalls)
			}
			// The adapter reports what arrived and nothing more. Turning an
			// absence of arguments into {} belongs to the caller, which can only
			// do it safely because the stream reaching here means it was whole.
			if got := resp.ToolCalls[0].Arguments; got != "" {
				t.Fatalf("arguments = %q, want the empty input the provider actually sent", got)
			}
		})
	}

	t.Run("openai", func(t *testing.T) {
		body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"snooze","arguments":""}}]}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n"

		client := openAIFramingClient(t, body)
		resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
		if err != nil {
			t.Fatalf("a terminated stream with an empty arguments string is a complete call: %v", err)
		}
		if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "snooze" {
			t.Fatalf("tool calls = %+v, want snooze", resp.ToolCalls)
		}
		if got := resp.ToolCalls[0].Arguments; got != "" {
			t.Fatalf("arguments = %q, want the empty arguments the provider actually sent", got)
		}
	})
}

// Classifying the error as retryable is only worth anything if the retry
// machinery acts on it, so drive a real client through a gateway that drops the
// first body and answers the second.
func TestStreamFramingTruncationIsRetriedNotSurfaced(t *testing.T) {
	complete := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		attempts++
		if attempts == 1 {
			return // upstream lost; body closes clean and empty
		}
		_, _ = w.Write([]byte(complete))
	}))
	t.Cleanup(srv.Close)

	client, err := New(Options{
		Kind: "openai-compatible", BaseURL: srv.URL, APIKey: "k",
		HTTPClient: srv.Client(), Retries: 1, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("a dropped first body should have been retried, not surfaced: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want the cut stream to cost one retry", attempts)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"path":"a"}` {
		t.Fatalf("tool calls = %+v, want the call the second attempt sent", resp.ToolCalls)
	}
}

// severingServer drops the socket mid-body instead of ending it. A handler that
// simply returns produces a clean EOF at the client, which is a different
// failure from a reset or a chunked frame that never lands: those surface as a
// read error out of the scanner rather than as an orderly end of body.
func severingServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
		fmt.Fprintf(buf, "%x\r\n%s\r\n", len(body), body)
		_ = buf.Flush()
		// No terminating zero-length chunk: the body is severed, not finished.
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The physical form of this failure is usually a reset or a half-sent chunk,
// not a tidy end of body. That error comes back from the scanner and used to be
// returned raw, ahead of the terminal check and matching nothing Retryable
// looks for, so the very case the terminal requirement exists to catch became a
// hard turn error instead of a retry.
func TestStreamFramingSeveredConnectionIsRetryable(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		frame := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"/tm"}}]}}]}` + "\n\n"
		srv := severingServer(t, frame)
		client := &openAIClient{opts: Options{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}, vendor: "compat"}

		resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
		assertSeveredStream(t, resp, err)
	})

	t.Run("anthropic", func(t *testing.T) {
		frame := "event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write_file","input":{}}}` + "\n\n"
		srv := severingServer(t, frame)
		client := &anthropicClient{opts: Options{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client()}}

		resp, err := client.Stream(context.Background(), Request{Model: "claude"}, func(Event) error { return nil })
		assertSeveredStream(t, resp, err)
	})
}

func assertSeveredStream(t *testing.T, resp *Response, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("a severed connection was accepted as a complete answer: %+v", resp)
	}
	if !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("a read that died mid-body is a truncated stream, got %#v", err)
	}
	if !Retryable(err) {
		t.Fatalf("a severed stream must reach the turn-level retry, got %v", err)
	}
	assertNoPartialAnswer(t, resp)
}

// The token cap can fall between a tool call's name and its arguments. The
// stream is framed correctly in that case — finish_reason and [DONE] both
// arrive — so framing vouches for nothing and only the finish reason says the
// answer stopped short of what the model meant to say.
func TestStreamFramingTokenCapDoesNotCompleteAToolCall(t *testing.T) {
	t.Run("a call whose arguments never came is refused", func(t *testing.T) {
		body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"write_file","arguments":""}}]}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" +
			"data: [DONE]\n\n"

		client := openAIFramingClient(t, body)
		resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
		if err == nil {
			t.Fatalf("write_file with no arguments was accepted because the framing was tidy: %+v", resp)
		}
		if !Retryable(err) {
			t.Fatalf("a cap-truncated call must reach the turn-level retry, got %v", err)
		}
		assertNoPartialAnswer(t, resp)
	})

	// The cap is not by itself a reason to throw the turn away: a call whose
	// arguments did arrive is as usable as any other.
	t.Run("a call whose arguments did come survives", func(t *testing.T) {
		body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" +
			"data: [DONE]\n\n"

		client := openAIFramingClient(t, body)
		resp, err := client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(Event) error { return nil })
		if err != nil {
			t.Fatalf("a cap that fell after the arguments still leaves a usable call: %v", err)
		}
		if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != `{"path":"a"}` {
			t.Fatalf("tool calls = %+v, want the call the provider finished sending", resp.ToolCalls)
		}
	})
}

// The retry that fires in production is the one the client declines to make.
// Once tokens have reached the caller, replaying would show the answer twice,
// so the client stops and hands the error up for the agent's turn loop, which
// resets the partial reply before asking again. Pin that division of labour:
// the client must not retry, and the error must stay retryable for the layer
// that can.
func TestStreamFramingCutAfterTokensIsHandedToTheCaller(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		attempts++
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"I will now "}}]}` + "\n\n"))
	}))
	t.Cleanup(srv.Close)

	client, err := New(Options{
		Kind: "openai-compatible", BaseURL: srv.URL, APIKey: "k",
		HTTPClient: srv.Client(), Retries: 3, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var shown []string
	_, err = client.Stream(context.Background(), Request{Model: "glm-5.2"}, func(e Event) error {
		if e.Type == EventText {
			shown = append(shown, e.Delta)
		}
		return nil
	})
	if err == nil {
		t.Fatal("a body cut after the first token was accepted as a complete answer")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want the client to stop once tokens had been shown", attempts)
	}
	if len(shown) != 1 {
		t.Fatalf("emitted %d text events, want the partial reply shown once and not replayed", len(shown))
	}
	if !Retryable(err) {
		t.Fatalf("the agent turn loop only retries what Retryable admits, got %v", err)
	}
}

// A cut stream must hand back no answer at all, not an answer plus an error.
// Returning the partial one would leave the fabricated call the accumulator
// used to invent — write_file with no path — within reach of any caller that
// reads the response before the error, which is a thing callers do.
func assertNoPartialAnswer(t *testing.T, resp *Response) {
	t.Helper()
	if resp != nil {
		t.Fatalf("a cut stream returned a usable answer beside its error: %+v", resp)
	}
}
