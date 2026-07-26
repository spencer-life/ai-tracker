package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spf13/cobra"
)

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

var doctorJSON bool

var doctorCmd = &cobra.Command{Use: "doctor", Short: "Diagnose telemetry freshness, quality, and local security", RunE: func(cmd *cobra.Command, args []string) error {
	dbConn, err := ingest.InitDB()
	if err != nil {
		return err
	}
	defer func() { _ = dbConn.Close() }()
	checks := []doctorCheck{}
	dir, _ := ingest.DataDir()
	for _, path := range []string{dir, filepath.Join(dir, "data.db")} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			checks = append(checks, doctorCheck{filepath.Base(path), "error", statErr.Error()})
			continue
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		status := "ok"
		if info.Mode().Perm() != want {
			status = "warn"
		}
		checks = append(checks, doctorCheck{filepath.Base(path), status, fmt.Sprintf("permissions %04o (expected %04o)", info.Mode().Perm(), want)})
	}
	for _, q := range []struct{ name, sql string }{
		{"unknown pricing", `SELECT COUNT(*) FROM usage_events WHERE cost_micros IS NULL AND total_tokens > 0`},
		{"estimated usage", `SELECT COUNT(*) FROM usage_events WHERE measurement='estimated'`},
		{"latest sync errors", `SELECT COALESCE((SELECT error_count FROM sync_runs ORDER BY id DESC LIMIT 1), 0)`},
	} {
		var n int64
		if err := dbConn.QueryRowContext(cmd.Context(), q.sql).Scan(&n); err != nil && !isNoRows(err) {
			return err
		}
		status := "ok"
		if n > 0 {
			status = "warn"
		}
		checks = append(checks, doctorCheck{q.name, status, fmt.Sprintf("%d records", n)})
	}
	for index := range checks {
		if checks[index].Name != "unknown pricing" || checks[index].Status == "ok" {
			continue
		}
		rows, err := dbConn.QueryContext(cmd.Context(), `SELECT CASE WHEN model='' THEN '(unknown model)' ELSE model END,COUNT(*),COALESCE(SUM(total_tokens),0) FROM usage_events WHERE cost_micros IS NULL AND total_tokens>0 GROUP BY model ORDER BY SUM(total_tokens) DESC LIMIT 5`)
		if err != nil {
			return err
		}
		var models []string
		for rows.Next() {
			var model string
			var events, tokens int64
			if err := rows.Scan(&model, &events, &tokens); err != nil {
				_ = rows.Close()
				return err
			}
			models = append(models, fmt.Sprintf("%s (%d events, %d tokens)", model, events, tokens))
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(models) > 0 {
			checks[index].Detail += "; top models: " + strings.Join(models, ", ")
		}
	}
	if doctorJSON {
		return json.NewEncoder(os.Stdout).Encode(checks)
	}
	for _, check := range checks {
		fmt.Printf("%-5s %-18s %s\n", check.Status, check.Name, check.Detail)
	}
	return nil
}}

func isNoRows(err error) bool { return err == sql.ErrNoRows }
func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit JSON")
	rootCmd.AddCommand(doctorCmd)
}
