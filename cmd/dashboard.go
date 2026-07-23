package cmd

import (
	"fmt"
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

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start the embedded AI Tracker Catppuccin Web Dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		dbConn, err := ingest.InitDB()
		if err != nil {
			fmt.Printf("Error initializing database: %v\n", err)
			os.Exit(1)
		}
		
		defer dbConn.Close()

		addr := dashboardHost + ":" + dashboardPort
		url := "http://" + addr
		fmt.Printf("⚡ AI Tracker Telemetry Dashboard (Catppuccin Frappe)\n")
		fmt.Printf("🌐 Embedded Web Interface: %s/\n", url)
		fmt.Printf("📡 Telemetry WebSocket API: ws://%s/ws\n", addr)

		if dashboardOpen {
			go func() {
				time.Sleep(100 * time.Millisecond)
				exec.Command("xdg-open", url).Start()
			}()
		}

		if err := web.StartServer(addr); err != nil {
			fmt.Printf("Error starting dashboard server: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	dashboardCmd.Flags().StringVarP(&dashboardPort, "port", "p", "8080", "Port to serve web dashboard")
	dashboardCmd.Flags().StringVar(&dashboardHost, "host", "127.0.0.1", "Host to serve web dashboard")
	dashboardCmd.Flags().BoolVar(&dashboardOpen, "open", false, "Open dashboard in local browser")
	rootCmd.AddCommand(dashboardCmd)
}
