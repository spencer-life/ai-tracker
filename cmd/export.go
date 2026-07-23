package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spencer-life/ai-tracker/ingest"
)

var (
	exportJson  bool
	exportDays  int
	exportAgent string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export telemetry data from the local SQLite DB",
	Run: func(cmd *cobra.Command, args []string) {
		db, err := ingest.InitDB()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing DB: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		query := "SELECT id, agent, timestamp, model, input_tokens, output_tokens, cost FROM token_logs WHERE 1=1"
		var queryArgs []interface{}

		if exportDays > 0 {
			timeAgo := time.Now().AddDate(0, 0, -exportDays)
			query += " AND timestamp >= ?"
			queryArgs = append(queryArgs, timeAgo)
		}

		if exportAgent != "" {
			query += " AND agent = ?"
			queryArgs = append(queryArgs, exportAgent)
		}

		rows, err := db.Query(query, queryArgs...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying DB: %v\n", err)
			os.Exit(1)
		}
		defer rows.Close()

		var logs []ingest.TokenLog
		for rows.Next() {
			var log ingest.TokenLog
			if err := rows.Scan(&log.ID, &log.Agent, &log.Timestamp, &log.Model, &log.Input, &log.Output, &log.Cost); err != nil {
				fmt.Fprintf(os.Stderr, "Error scanning row: %v\n", err)
				continue
			}
			logs = append(logs, log)
		}

		if err = rows.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Row iteration error: %v\n", err)
			os.Exit(1)
		}

		if exportJson {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(logs); err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
				os.Exit(1)
			}
		} else {
			// By default, just dump raw JSON if it's for jq/agents
			encoder := json.NewEncoder(os.Stdout)
			if err := encoder.Encode(logs); err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
				os.Exit(1)
			}
		}
	},
}

func init() {
	exportCmd.Flags().BoolVar(&exportJson, "json", false, "Output as JSON")
	exportCmd.Flags().IntVar(&exportDays, "days", 0, "Filter by last N days")
	exportCmd.Flags().StringVar(&exportAgent, "agent", "", "Filter by agent name")
	rootCmd.AddCommand(exportCmd)
}
