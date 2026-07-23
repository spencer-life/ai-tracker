package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spencer-life/ai-tracker/tui"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Start the AI Tracker Catppuccin Frappe TUI",
	Run: func(cmd *cobra.Command, args []string) {
		p := tea.NewProgram(tui.NewModel(), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Printf("Error running Catppuccin TUI: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
