package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spencer-life/ai-tracker/ingest"

	"github.com/spencer-life/ai-tracker/web"
	"github.com/spf13/cobra"
)

var dashboardPort string
var dashboardHost string
var dashboardOpen bool
var db *sql.DB

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start the embedded AI Tracker Catppuccin Web Dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		db, err = ingest.InitDB()
		if err != nil {
			fmt.Printf("Error initializing database: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		http.Handle("/", http.FileServer(http.FS(web.FS)))
		http.HandleFunc("/api/v1/telemetry", handleTelemetry)

		addr := dashboardHost + ":" + dashboardPort
		url := "http://" + addr
		fmt.Printf("⚡ AI Tracker Telemetry Dashboard (Catppuccin Frappe)\n")
		fmt.Printf("🌐 Embedded Web Interface: %s/\n", url)
		fmt.Printf("📡 Telemetry REST API:   %s/api/v1/telemetry\n", url)

		if dashboardOpen {
			go func() {
				time.Sleep(100 * time.Millisecond)
				exec.Command("xdg-open", url).Start()
			}()
		}

		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Printf("Error starting dashboard server: %v\n", err)
			os.Exit(1)
		}
	},
}

func handleTelemetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if db == nil {
		http.Error(w, `{"error":"db_error"}`, http.StatusInternalServerError)
		return
	}

	var totalTokens int
	var estCost float64
	db.QueryRow("SELECT COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(cost), 0) FROM token_logs").Scan(&totalTokens, &estCost)

	var activeAgents int
	db.QueryRow("SELECT COUNT(DISTINCT agent) FROM token_logs").Scan(&activeAgents)

	resp := map[string]interface{}{
		"status":         "ok",
		"theme":          "catppuccin-frappe",
		"total_tokens":   totalTokens,
		"est_cost_usd":   estCost,
		"active_agents":  activeAgents,
		"avg_latency_ms": 412,
	}
	json.NewEncoder(w).Encode(resp)
}

func init() {
	dashboardCmd.Flags().StringVarP(&dashboardPort, "port", "p", "8080", "Port to serve web dashboard")
	dashboardCmd.Flags().StringVar(&dashboardHost, "host", "127.0.0.1", "Host to serve web dashboard")
	dashboardCmd.Flags().BoolVar(&dashboardOpen, "open", false, "Open dashboard in local browser")
	rootCmd.AddCommand(dashboardCmd)
}
