package cmd

import (
	"fmt"
	"log"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync logs and update the SQLite database",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Running sync...")
		if err := ingest.IngestLogs(); err != nil {
			log.Fatalf("Sync failed: %v", err)
		}
		fmt.Println("Sync complete!")
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
