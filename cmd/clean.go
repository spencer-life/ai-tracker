package cmd

import (
	"fmt"
	"log"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Safely wipe the telemetry database tables",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Cleaning database...")
		db, err := ingest.InitDB()
		if err != nil {
			log.Fatalf("Failed to open DB: %v", err)
		}
		defer db.Close()

		_, err = db.Exec("DELETE FROM token_logs; VACUUM;")
		if err != nil {
			log.Fatalf("Failed to clear token_logs table: %v", err)
		}
		fmt.Println("Database cleaned!")
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
