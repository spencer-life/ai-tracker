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
		dbConn, err := ingest.InitDB()
		repo := ingest.NewRepository(dbConn)
		if err != nil {
			log.Fatalf("Failed to open DB: %v", err)
		}
		defer dbConn.Close()

		_, err = repo.GetDB().Exec("DELETE FROM token_logs; VACUUM;")
		if err != nil {
			log.Fatalf("Failed to clear token_logs table: %v", err)
		}
		fmt.Println("Database cleaned!")
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
