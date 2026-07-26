package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spencer-life/ai-tracker/ingest"
	"github.com/spencer-life/ai-tracker/web"
	"github.com/spf13/cobra"
)

var dashboardPort string
var dashboardHost string
var dashboardOpen bool
var dashboardNoSync bool

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Start the embedded AI Tracker Catppuccin Web Dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		dbConn, err := ingest.InitDB()
		if err != nil {
			fmt.Printf("Error initializing database: %v\n", err)
			os.Exit(1)
		}

		defer func() { _ = dbConn.Close() }()

		repo := ingest.NewRepository(dbConn)
		if !dashboardNoSync {
			if report, syncErr := ingest.Sync(cmd.Context(), dbConn, ingest.SyncOptions{}); syncErr != nil {
				fmt.Printf("Initial sync %s with %d diagnostics: %v\n", report.Status, len(report.Diagnostics), syncErr)
			}
		}
		addr := dashboardHost + ":" + dashboardPort
		url := "http://" + addr
		fmt.Printf("⚡ AI Tracker Telemetry Dashboard (Catppuccin Frappe)\n")
		fmt.Printf("🌐 Embedded Web Interface: %s/\n", url)
		fmt.Printf("📡 Live update stream: http://%s/api/v2/events\n", addr)

		if dashboardOpen {
			go func() {
				time.Sleep(100 * time.Millisecond)
				name, args, ok := browserOpenCommand(runtime.GOOS, url)
				if ok {
					_ = exec.Command(name, args...).Start()
				}
			}()
		}

		syncFn := func(ctx context.Context) error {
			_, syncErr := ingest.Sync(ctx, dbConn, ingest.SyncOptions{})
			return syncErr
		}
		if err := web.StartServer(addr, repo, syncFn); err != nil {
			fmt.Printf("Error starting dashboard server: %v\n", err)
			os.Exit(1)
		}
	},
}

func browserOpenCommand(goos, target string) (string, []string, bool) {
	switch goos {
	case "darwin":
		return "open", []string{target}, true
	case "linux":
		return "xdg-open", []string{target}, true
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}, true
	default:
		return "", nil, false
	}
}

func init() {
	dashboardCmd.Flags().StringVarP(&dashboardPort, "port", "p", "8080", "Port to serve web dashboard")
	dashboardCmd.Flags().StringVar(&dashboardHost, "host", "127.0.0.1", "Host to serve web dashboard")
	dashboardCmd.Flags().BoolVar(&dashboardOpen, "open", false, "Open dashboard in local browser")
	dashboardCmd.Flags().BoolVar(&dashboardNoSync, "no-sync", false, "Serve existing data without an initial source sync")
	rootCmd.AddCommand(dashboardCmd)
}
