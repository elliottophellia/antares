package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/enowdev/antares/internal/tui"
)

func main() {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	fmt.Print(tui.RenderDemoFrame(104, 36))
}
