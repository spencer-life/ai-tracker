package cmd

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spencer-life/ai-tracker/internal/db"
)

var (
	exportJson  bool
	exportDays  int
	exportAgent string
	exportFrom  string
	exportTo    string
	exportCsv   bool
	exportOut   string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export telemetry data from the local SQLite DB",
	Run: func(cmd *cobra.Command, args []string) {
		dbConn, err := ingest.InitDB()
		repo := ingest.NewRepository(dbConn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing DB: %v\n", err)
			os.Exit(1)
		}
		defer dbConn.Close()

		query := "SELECT id, agent, timestamp, model, input_tokens, output_tokens, cost FROM token_logs WHERE 1=1"
		var queryArgs []interface{}

		if exportDays > 0 {
			timeAgo := time.Now().AddDate(0, 0, -exportDays)
			query += " AND timestamp >= ?"
			queryArgs = append(queryArgs, timeAgo)
		}

		if exportFrom != "" {
			if t, err := time.Parse(time.RFC3339, exportFrom); err == nil {
				query += " AND timestamp >= ?"
				queryArgs = append(queryArgs, t)
			} else if t, err := time.Parse("2006-01-02", exportFrom); err == nil {
				query += " AND timestamp >= ?"
				queryArgs = append(queryArgs, t)
			}
		}

		if exportTo != "" {
			if t, err := time.Parse(time.RFC3339, exportTo); err == nil {
				query += " AND timestamp <= ?"
				queryArgs = append(queryArgs, t)
			} else if t, err := time.Parse("2006-01-02", exportTo); err == nil {
				t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
				query += " AND timestamp <= ?"
				queryArgs = append(queryArgs, t)
			}
		}

		if exportAgent != "" {
			query += " AND agent = ?"
			queryArgs = append(queryArgs, exportAgent)
		}

		rows, err := repo.GetDB().Query(query, queryArgs...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying DB: %v\n", err)
			os.Exit(1)
		}
		defer rows.Close()

		var logs []db.TokenLog
		for rows.Next() {
			var log db.TokenLog
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

		var output []byte
		if exportCsv {
			var buf strings.Builder
			writer := csv.NewWriter(&buf)
			writer.Write([]string{"ID", "Agent", "Timestamp", "Model", "InputTokens", "OutputTokens", "Cost"})
			for _, log := range logs {
				writer.Write([]string{
					strconv.Itoa(log.ID),
					log.Agent,
					log.Timestamp.Format(time.RFC3339),
					log.Model,
					strconv.Itoa(log.Input),
					strconv.Itoa(log.Output),
					fmt.Sprintf("%f", log.Cost),
				})
			}
			writer.Flush()
			output = []byte(buf.String())
		} else {
			var err error
			if exportJson {
				output, err = json.MarshalIndent(logs, "", "  ")
			} else {
				output, err = json.Marshal(logs)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
				os.Exit(1)
			}
		}

		if exportOut != "" {
			if strings.HasSuffix(strings.ToLower(exportOut), ".zip") {
				zipFile, err := os.Create(exportOut)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error creating zip file: %v\n", err)
					os.Exit(1)
				}
				defer zipFile.Close()

				zipWriter := zip.NewWriter(zipFile)

				filename := "data.json"
				if exportCsv {
					filename = "data.csv"
				}

				f, err := zipWriter.Create(filename)
				if err != nil {
					zipWriter.Close()
					fmt.Fprintf(os.Stderr, "Error creating zip entry: %v\n", err)
					os.Exit(1)
				}

				_, err = f.Write(output)
				if err != nil {
					zipWriter.Close()
					fmt.Fprintf(os.Stderr, "Error writing zip entry: %v\n", err)
					os.Exit(1)
				}
				
				if err := zipWriter.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "Error closing zip writer: %v\n", err)
					os.Exit(1)
				}
			} else {
				err := os.WriteFile(exportOut, output, 0644)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
					os.Exit(1)
				}
			}
		} else {
			fmt.Print(string(output))
		}
	},
}

func init() {
	exportCmd.Flags().BoolVar(&exportJson, "json", false, "Output as JSON")
	exportCmd.Flags().IntVar(&exportDays, "days", 0, "Filter by last N days")
	exportCmd.Flags().StringVar(&exportAgent, "agent", "", "Filter by agent name")
	exportCmd.Flags().StringVar(&exportFrom, "from", "", "Filter by start date (YYYY-MM-DD or RFC3339)")
	exportCmd.Flags().StringVar(&exportTo, "to", "", "Filter by end date (YYYY-MM-DD or RFC3339)")
	exportCmd.Flags().BoolVar(&exportCsv, "csv", false, "Output as CSV")
	exportCmd.Flags().StringVar(&exportOut, "out", "", "Output file path (use .zip for compressed archive)")
	rootCmd.AddCommand(exportCmd)
}
