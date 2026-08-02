package socialbrowser

import (
	"context"
	"fmt"

	cloak "github.com/enowdev/cloak-go"
)

func init() {
	launchCloakBrowser = realLaunchCloak
}

// realLaunchCloak launches a stealth Chromium via cloak-go with a persistent
// user-data-dir and stable fingerprint seed.
func realLaunchCloak(ctx context.Context, profileDir, seed string) (interface{ Close() }, context.CancelFunc, error) {
	opts := cloak.LaunchOptions{
		Headless:        false,
		StealthArgs:     true,
		StartMaximized:  true,
		UserDataDir:     profileDir,
		FingerprintSeed: seed,
	}

	browser, err := cloak.Launch(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("cloak launch: %w", err)
	}

	// The cancel function tears down the browser context. The Manager calls
	// it from Stop(); the Browser.Close() kills the Chrome process.
	cancel := context.CancelFunc(func() {
		// The browser context cancel is handled by Browser.Close().
	})

	return browser, cancel, nil
}
