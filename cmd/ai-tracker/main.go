package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spencer-life/ai-tracker/pkg/tui"
	"github.com/spencer-life/ai-tracker/web"
)

func main() {
	tuiMode := flag.Bool("tui", false, "Run in terminal interactive TUI mode")
	port := flag.String("port", "8080", "HTTP web dashboard port")
	flag.Parse()

	if *tuiMode {
		runTUI()
		return
	}

	http.Handle("/", http.FileServer(http.FS(web.FS)))
	http.HandleFunc("/api/v1/telemetry", handleTelemetry)

	addr := ":" + *port
	fmt.Printf("⚡ AI Tracker Telemetry Engine (Catppuccin Frappe) starting...\n")
	fmt.Printf("🌐 Embedded Web Dashboard: http://localhost%s/\n", addr)
	fmt.Printf("🖥️  Run TUI directly with:   go run ./cmd/tui  or  go run ./cmd/ai-tracker -tui\n")

	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Printf("HTTP Server failed: %v\n", err)
		os.Exit(1)
	}
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

func runTUI() {
	p := tea.NewProgram(tui.NewModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error starting TUI: %v\n", err)
		os.Exit(1)
	}
}
