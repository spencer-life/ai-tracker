package cmd

import (
	"fmt"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spf13/cobra"
)

var cleanYes bool

var cleanCmd = &cobra.Command{Use: "clean", Short: "Clear telemetry facts and checkpoints together", RunE: func(cmd *cobra.Command, args []string) error {
	if !cleanYes {
		return fmt.Errorf("clean requires --yes; use `ait sync --rebuild` when you want a recoverable refresh")
	}
	dbConn, err := ingest.InitDB()
	if err != nil {
		return err
	}
	defer func() { _ = dbConn.Close() }()
	repo := ingest.NewRepository(dbConn)
	if _, err := repo.Backup("pre-clean"); err != nil {
		return fmt.Errorf("backup before clean: %w", err)
	}
	if err := repo.ClearAll(cmd.Context()); err != nil {
		return err
	}
	fmt.Println("telemetry and source checkpoints cleared; backup retained")
	return nil
}}

func init() {
	cleanCmd.Flags().BoolVar(&cleanYes, "yes", false, "confirm clearing telemetry and checkpoints")
	rootCmd.AddCommand(cleanCmd)
}
