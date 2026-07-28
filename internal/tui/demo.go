package tui

import (
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/config"
)

// NewDemo builds a self-contained TUI seeded with representative content and no
// live agent — for previewing the interface (and for hot-reload development).
// Sending a message produces a canned reply instead of calling a model.
func NewDemo() *Model {
	cfg := &config.Config{}
	cfg.Model.Default = "claude-sonnet-5"
	cfg.Model.Provider = "anthropic"
	cfg.Display.ShowReasoning = true

	ta := textarea.New()
	ta.Placeholder = "Message antares…  (/ for commands, Enter to send, Ctrl+J for newline)"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()
	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(themeByName(defaultTheme).Accent)
	ta.Cursor.SetMode(cursor.CursorHide)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		cfg:           cfg,
		themeName:     defaultTheme,
		st:            newStyles(themeByName(defaultTheme)),
		ta:            ta,
		spin:          sp,
		showReasoning: true,
		demo:          true,
		title:         "Auth middleware refactor",
		sessionID:     "ses_demo00000001",
		tokensIn:      1840,
		tokensOut:     512,
	}
	m.seedDemo()
	return m
}

func (m *Model) seedDemo() {
	m.blocks = []block{
		{kind: blockSystem, text: "antares 1.0.0 — type a message, or /help for commands."},
		{kind: blockUser, text: "Refactor the auth middleware to verify tokens through the new store, and add a test for the failure path."},
		{kind: blockReasoning, text: "The middleware only checks the header is non-empty. I should thread the token store through, verify the token, and put the subject on the request context.", done: true},
		{kind: blockAssistant, done: true, text: "Here's the plan:\n\n- Thread `*token.Store` into `Authenticate`\n- Replace the empty-string check with a real `store.Verify` call\n- Put the verified subject on the request `context`\n\nLet me look at the code first."},
		{kind: blockTool, title: "read_file  internal/auth/middleware.go", done: true, text: "func Authenticate(next http.Handler) http.Handler {\n    // ... 10 lines ...\n}"},
		{kind: blockTool, title: "edit_file  internal/auth/middleware.go", done: true, text: "edited internal/auth/middleware.go (+5 -3)"},
		{kind: blockTool, title: "terminal  go test ./internal/auth/...", done: true, text: "ok  \tgithub.com/enowdev/antares/internal/auth\t0.184s"},
		{kind: blockAssistant, done: true, text: "Done. The middleware now verifies through the store, and the failure path is covered:\n\n```go\nfunc TestAuthenticate_RejectsBadToken(t *testing.T) {\n\treq := httptest.NewRequest(\"GET\", \"/\", nil)\n\treq.Header.Set(\"Authorization\", \"bad\")\n\t// ...\n}\n```\n\nAll tests pass. ✅"},
	}
}

// demoReply appends a canned assistant answer, so the preview stays interactive
// without a model.
func (m *Model) demoReply(text string) {
	m.blocks = append(m.blocks,
		block{kind: blockUser, text: text},
		block{kind: blockReasoning, done: true, text: "This is a preview build — echoing a canned reply so the layout can be seen without a live model."},
		block{kind: blockAssistant, done: true, text: "**Preview mode** — no model is attached.\n\nYou typed:\n\n> " + text + "\n\nEverything you see here (sidebar, Markdown, tools, reasoning) is rendered by the real TUI. Toggle reasoning with **Ctrl+R**, scroll with **PgUp/PgDn**, and try **/help**."},
	)
}

// RenderWelcomeFrame renders the empty-state splash at a given animation frame —
// used by the screenshot tool to preview the welcome screen.
func RenderWelcomeFrame(w, h, frame int) string {
	m := NewDemo()
	m.blocks = nil
	m.title, m.sessionID = "", ""
	m.welcomeFrame = frame
	m.width, m.height = w, h
	m.ready = true
	m.layout()
	m.refreshTranscript()
	return m.View()
}

// RenderThemeModalFrame renders the demo with the theme picker open — used by
// the screenshot tool to preview the modal.
func RenderThemeModalFrame(w, h int) string {
	m := NewDemo()
	m.width, m.height = w, h
	m.ready = true
	m.layout()
	m.refreshTranscript()
	m.openThemePicker()
	return m.View()
}

// RenderProviderModalFrame previews the provider picker.
func RenderProviderModalFrame(w, h int) string {
	m := NewDemo()
	m.width, m.height = w, h
	m.ready = true
	m.layout()
	m.refreshTranscript()
	m.openProviderPicker()
	return m.View()
}

// RenderModelModalFrame previews the model picker with a seeded provider.
func RenderModelModalFrame(w, h int) string {
	m := NewDemo()
	m.cfg.Providers = map[string]config.Provider{
		"anthropic": {Kind: "anthropic", Enabled: true,
			Models: []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"}},
	}
	m.width, m.height = w, h
	m.ready = true
	m.layout()
	m.refreshTranscript()
	m.openModelPicker()
	return m.View()
}

// RenderDemoFrame renders a single demo frame at the given size — used by the
// screenshot tool to preview the design.
func RenderDemoFrame(w, h int) string {
	m := NewDemo()
	m.width, m.height = w, h
	m.ready = true
	m.layout()
	m.refreshTranscript()
	m.vp.GotoTop()
	return m.View()
}
