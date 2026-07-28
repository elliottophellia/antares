package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/enowdev/antares/internal/tui"
)

func main() {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	if len(os.Args) > 1 && os.Args[1] == "welcome" {
		frame := 0
		if len(os.Args) > 2 {
			frame, _ = strconv.Atoi(os.Args[2])
		}
		fmt.Print(tui.RenderWelcomeFrame(104, 36, frame))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "theme" {
		fmt.Print(tui.RenderThemeModalFrame(104, 36))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "provider" {
		fmt.Print(tui.RenderProviderModalFrame(104, 36))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "model" {
		fmt.Print(tui.RenderModelModalFrame(104, 36))
		return
	}
	fmt.Print(tui.RenderDemoFrame(104, 36))
}
