package tools

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// visionTool describes an image the agent found on disk, using a vision model.
// It is how a text-only conversation reads a screenshot, a diagram, or a photo.
type visionTool struct{}

func (visionTool) Name() string { return "view_image" }

func (visionTool) Description() string {
	return "Look at an image file and describe what is in it — a screenshot, a diagram, a photo, a chart. " +
		"Also transcribes any text it contains. Use it to read an image the browser or shell produced, " +
		"or one the user pointed you at."
}

func (visionTool) Schema() map[string]any {
	return schema(map[string]any{
		"path":     prop("string", "Path to the image file to look at."),
		"question": prop("string", "Optional: what to look for or answer about the image."),
	}, "path")
}

func (visionTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Path     string `json:"path"`
		Question string `json:"question"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Vision == nil {
		return Errorf("vision is not available — configure a vision-capable model")
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return Errorf("path is required")
	}
	// Resolve a relative path against the workspace.
	if !filepath.IsAbs(path) && in.Workspace != "" {
		path = filepath.Join(in.Workspace, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Errorf("could not read the image: %v", err)
	}
	if len(data) > 20<<20 {
		return Errorf("the image is too large (%d MB) to send", len(data)/(1<<20))
	}

	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return Errorf("%s does not look like an image (%s)", path, mime)
	}

	in.Emit(Progress{Tool: "view_image", Message: "looking…"})
	desc, err := in.Deps.Vision(ctx, data, mime, args.Question)
	if err != nil {
		return Errorf("could not analyse the image: %v", err)
	}
	if strings.TrimSpace(desc) == "" {
		return Text("The model returned no description of the image.")
	}
	return Text(desc)
}
