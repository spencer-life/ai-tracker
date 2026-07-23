package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spencer-life/ai-tracker/pkg/tui"
)

func main() {
	p := tea.NewProgram(tui.NewModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error starting Catppuccin TUI: %v\n", err)
		os.Exit(1)
	}
}
