package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spencer-life/ai-tracker/pkg/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Start the AI Tracker Catppuccin Frappe TUI",
	RunE: func(cmd *cobra.Command, args []string) error {
		dbConn, err := ingest.InitDB()
		if err != nil {
			return fmt.Errorf("open AI Tracker database: %w", err)
		}
		repo := ingest.NewRepository(dbConn)
		defer func() { _ = repo.Close() }()

		p := tea.NewProgram(tui.NewModel(repo), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("run AI Tracker TUI: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
