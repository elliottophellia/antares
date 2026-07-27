package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/llm"
)

// cmdAuth handles interactive provider sign-in flows.
func cmdAuth(args []string) error {
	if len(args) == 0 {
		fmt.Println(`Sign in to a provider:
  antares auth copilot    GitHub Copilot (device flow)`)
		return nil
	}
	switch strings.ToLower(args[0]) {
	case "copilot", "github-copilot":
		return authCopilot()
	default:
		return fmt.Errorf("unknown auth target %q (try: copilot)", args[0])
	}
}

// authCopilot runs the GitHub device flow and prints the token to store as the
// copilot provider's api_key.
func authCopilot() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dc, err := llm.StartCopilotLogin(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("\nTo authorise Antares for GitHub Copilot:\n\n")
	fmt.Printf("  1. Open %s\n", dc.VerificationURI)
	fmt.Printf("  2. Enter the code: %s\n\n", dc.UserCode)
	fmt.Println("Waiting for authorisation…")

	token, err := llm.PollCopilotToken(ctx, dc)
	if err != nil {
		return err
	}

	fmt.Printf("\nAuthorised. Configure the provider with:\n\n")
	fmt.Printf("  antares config set providers.copilot.kind copilot\n")
	fmt.Printf("  antares config set providers.copilot.api_key %s\n", token)
	fmt.Printf("  antares config set providers.copilot.enabled true\n\n")
	fmt.Println("Then pick a Copilot model, e.g. gpt-4o, with: antares model copilot/gpt-4o")
	return nil
}
