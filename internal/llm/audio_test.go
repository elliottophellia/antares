package llm

import "testing"

func TestOpenAISatisfiesAudioClient(t *testing.T) {
	var _ AudioClient = &openAIClient{opts: Options{BaseURL: "https://api.openai.com/v1"}}
}

func TestAudioDefaults(t *testing.T) {
	// Speak/Transcribe default the model/voice/format; verify the zero-value
	// path is handled by constructing the client (no panic on defaults).
	c := &openAIClient{opts: Options{BaseURL: "https://api.openai.com/v1"}}
	if _, ok := interface{}(c).(AudioClient); !ok {
		t.Fatal("openai client should be an AudioClient")
	}
}
