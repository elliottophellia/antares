package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// setSoulTool records the agent's identity (its "soul") from what the user says
// during the first-conversation interview. It writes ~/.antares/SOUL.md, which
// is folded into the system prompt on every turn everywhere (web, TUI,
// gateways) — so this is how the agent gets a name and a personality.
type setSoulTool struct{}

func (setSoulTool) Name() string { return "set_soul" }

func (setSoulTool) Description() string {
	return "Record your identity — your name and personality — after the user has told you who you should be. " +
		"Call this once you have enough from the identity interview (or whenever the user asks to change who you are). " +
		"It writes the global SOUL.md that defines you across every interface. Pass what you know; leave the rest blank."
}

func (setSoulTool) RequiresApproval() bool { return true }

func (setSoulTool) Schema() map[string]any {
	return schema(map[string]any{
		"name":       prop("string", "The name the user wants to call you, e.g. Vesper."),
		"calls_user": prop("string", "What you should call the user (their name or preferred address)."),
		"voice":      prop("string", "How you should talk: tone, length, formality — e.g. 'concise, warm, a little witty'."),
		"persona":    prop("string", "A short paragraph on your personality and character."),
		"principles": prop("string", "Values or rules you hold to, if any."),
		"quirks":     prop("string", "Small habits or flourishes that make you feel like you."),
		"raw":        prop("string", "Optional: the full SOUL.md markdown to write verbatim, instead of the fields above. Use only if the user dictated it directly."),
	}, "name")
}

func (setSoulTool) Execute(_ context.Context, in Input) Result {
	var args struct {
		Name       string `json:"name"`
		CallsUser  string `json:"calls_user"`
		Voice      string `json:"voice"`
		Persona    string `json:"persona"`
		Principles string `json:"principles"`
		Quirks     string `json:"quirks"`
		Raw        string `json:"raw"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}

	var body string
	if s := strings.TrimSpace(args.Raw); s != "" {
		body = s
	} else {
		if strings.TrimSpace(args.Name) == "" {
			return Errorf("a name is required (or pass raw with the full SOUL.md)")
		}
		body = renderSoul(args.Name, args.CallsUser, args.Voice, args.Persona, args.Principles, args.Quirks)
	}

	if err := config.SaveSoul(body); err != nil {
		return Errorf("could not save your identity: %v", err)
	}
	return Text(fmt.Sprintf("Identity saved — you are now %s. This SOUL.md defines you everywhere from now on.", strings.TrimSpace(args.Name)))
}

// renderSoul lays out the structured fields as a readable SOUL.md.
func renderSoul(name, callsUser, voice, persona, principles, quirks string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", strings.TrimSpace(name))
	if s := strings.TrimSpace(persona); s != "" {
		fmt.Fprintf(&b, "\n%s\n", s)
	}
	section := func(title, val string) {
		if s := strings.TrimSpace(val); s != "" {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", title, s)
		}
	}
	section("Voice & tone", voice)
	if s := strings.TrimSpace(callsUser); s != "" {
		fmt.Fprintf(&b, "\n## The user\n\nI call them %s.\n", s)
	}
	section("Principles", principles)
	section("Quirks", quirks)
	return strings.TrimSpace(b.String())
}
