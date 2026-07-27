// Command tuidemo previews the antares TUI with seeded content and no live
// model, for design work and hot-reload development.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/enowdev/antares/internal/tui"
)

func main() {
	if err := tui.NewDemo().Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
