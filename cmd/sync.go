package cmd

import (
	"fmt"
	"log"
	"time"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spf13/cobra"
)

var syncWatch bool

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync logs and update the SQLite database",
	Run: func(cmd *cobra.Command, args []string) {
		for {
			fmt.Println("Running sync...")
			if err := ingest.IngestLogs(); err != nil {
				log.Printf("Sync failed: %v", err)
				if !syncWatch {
					break
				}
				time.Sleep(5 * time.Second)
				continue
			}
			fmt.Println("Sync complete!")
			if !syncWatch {
				break
			}
			time.Sleep(5 * time.Second)
		}
	},
}

func init() {
	syncCmd.Flags().BoolVarP(&syncWatch, "watch", "w", false, "Watch for changes and sync continuously")
	rootCmd.AddCommand(syncCmd)
}
