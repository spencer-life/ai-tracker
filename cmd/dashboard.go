package cmd

import (
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"github.com/spencer-life/ai-tracker/web"
)

var dashboardPort string

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start the embedded AI Tracker Catppuccin Web Dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		http.Handle("/", http.FileServer(http.FS(web.FS)))
		http.HandleFunc("/api/v1/telemetry", handleTelemetry)

		addr := ":" + dashboardPort
		fmt.Printf("⚡ AI Tracker Telemetry Dashboard (Catppuccin Frappe)\n")
		fmt.Printf("🌐 Embedded Web Interface: http://localhost%s/\n", addr)
		fmt.Printf("📡 Telemetry REST API:   http://localhost%s/api/v1/telemetry\n", addr)

		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Printf("Error starting dashboard server: %v\n", err)
			os.Exit(1)
		}
	},
}

func handleTelemetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{
		"status": "ok",
		"theme": "catppuccin-frappe",
		"total_tokens": 4892150,
		"est_cost_usd": 18.42,
		"active_agents": 6,
		"avg_latency_ms": 412
	}`))
}

func init() {
	dashboardCmd.Flags().StringVarP(&dashboardPort, "port", "p", "8080", "Port to serve web dashboard")
	rootCmd.AddCommand(dashboardCmd)
}
