package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// ---- speak (text-to-speech) -------------------------------------------------

type speakTool struct{}

func (speakTool) Name() string { return "speak" }
func (speakTool) Description() string {
	return "Turn text into spoken audio and save it to a file. Use it to produce a voice reply or a narrated summary."
}
func (speakTool) Schema() map[string]any {
	return schema(map[string]any{
		"text":  prop("string", "The text to speak."),
		"voice": prop("string", "Optional voice name, e.g. alloy, echo, nova. Defaults to the configured voice."),
	}, "text")
}
func (speakTool) RequiresApproval() bool { return false }

func (speakTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if strings.TrimSpace(args.Text) == "" {
		return Errorf("text is required")
	}
	if in.Deps == nil || in.Deps.Speak == nil {
		return Errorf("voice output is not available in this runtime")
	}
	audio, ext, err := in.Deps.Speak(ctx, args.Text, args.Voice)
	if err != nil {
		return Errorf("could not synthesise speech: %v", err)
	}
	dir := filepath.Join(config.Home(), "audio")
	if in.Workspace != "" {
		if info, statErr := os.Stat(in.Workspace); statErr == nil && info.IsDir() {
			dir = filepath.Join(in.Workspace, ".antares", "audio")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Errorf("%v", err)
	}
	if ext == "" {
		ext = "mp3"
	}
	path := filepath.Join(dir, fmt.Sprintf("speech-%d.%s", time.Now().UnixMilli(), ext))
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return Errorf("%v", err)
	}
	return Result{
		Content: fmt.Sprintf("Saved spoken audio to %s (%d KB).", path, len(audio)/1024),
		Meta:    map[string]any{"path": path, "bytes": len(audio)},
	}
}

// ---- transcribe (speech-to-text) --------------------------------------------

type transcribeTool struct{}

func (transcribeTool) Name() string { return "transcribe" }
func (transcribeTool) Description() string {
	return "Transcribe an audio file (a voice message or recording) into text."
}
func (transcribeTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":  prop("string", "Path to an audio file to transcribe (relative to the workspace)."),
		"audio": prop("string", "Alternatively, base64-encoded audio bytes."),
	})
}
func (transcribeTool) RequiresApproval() bool { return false }

func (transcribeTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Path  string `json:"path"`
		Audio string `json:"audio"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Transcribe == nil {
		return Errorf("transcription is not available in this runtime")
	}

	var audio []byte
	filename := "audio.mp3"
	switch {
	case strings.TrimSpace(args.Path) != "":
		p, err := resolvePath(in.Workspace, args.Path)
		if err != nil {
			return Errorf("%v", err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return Errorf("cannot read %s: %v", args.Path, err)
		}
		audio = data
		filename = filepath.Base(p)
	case strings.TrimSpace(args.Audio) != "":
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(args.Audio))
		if err != nil {
			return Errorf("audio is not valid base64: %v", err)
		}
		audio = data
	default:
		return Errorf("either path or audio is required")
	}
	if len(audio) == 0 {
		return Errorf("the audio is empty")
	}

	text, err := in.Deps.Transcribe(ctx, filename, audio)
	if err != nil {
		return Errorf("could not transcribe: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		return Text("(no speech detected)")
	}
	return Text(text)
}
