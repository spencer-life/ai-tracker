package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spf13/cobra"
)

var syncWatch, syncRebuild, syncIncludeEstimates, syncJSON bool

func runSync(ctx context.Context, rebuild, includeEstimates bool) (ingest.SyncReport, error) {
	dbConn, err := ingest.InitDB()
	if err != nil {
		return ingest.SyncReport{}, err
	}
	defer func() { _ = dbConn.Close() }()
	repo := ingest.NewRepository(dbConn)
	if rebuild {
		if _, err := repo.Backup("pre-rebuild"); err != nil {
			return ingest.SyncReport{}, fmt.Errorf("backup before rebuild: %w", err)
		}
		if err := repo.ClearAll(ctx); err != nil {
			return ingest.SyncReport{}, fmt.Errorf("clear before rebuild: %w", err)
		}
	}
	return ingest.Sync(ctx, dbConn, ingest.SyncOptions{IncludeEstimates: includeEstimates})
}

var syncCmd = &cobra.Command{Use: "sync", Short: "Synchronize canonical local agent telemetry", RunE: func(cmd *cobra.Command, args []string) error {
	for {
		report, err := runSync(cmd.Context(), syncRebuild, syncIncludeEstimates)
		syncRebuild = false
		if syncJSON {
			_ = json.NewEncoder(os.Stdout).Encode(report)
		} else {
			fmt.Printf("sync %s: events=%d sessions=%d skipped=%d errors=%d diagnostics=%d\n", report.Status, report.EventsCommitted, report.SessionsCommitted, report.Skipped, report.Errors, len(report.Diagnostics))
			for _, d := range report.Diagnostics {
				fmt.Printf("  - %s\n", d)
			}
		}
		if err != nil && !syncWatch {
			return err
		}
		if !syncWatch {
			return nil
		}
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-time.After(5 * time.Second):
		}
	}
}}

func init() {
	syncCmd.Flags().BoolVarP(&syncWatch, "watch", "w", false, "continue syncing every five seconds")
	syncCmd.Flags().BoolVar(&syncRebuild, "rebuild", false, "back up and reimport canonical sources")
	syncCmd.Flags().BoolVar(&syncIncludeEstimates, "include-estimates", false, "include agy text-derived estimates")
	syncCmd.Flags().BoolVar(&syncJSON, "json", false, "emit sync reports as JSON")
	rootCmd.AddCommand(syncCmd)
}
