package web

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// FS embeds the index.html and static asset files for the embedded dashboard.
//go:embed index.html assets/js/*
var FS embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Broadcaster struct {
	clients   map[*websocket.Conn]bool
	clientsMu sync.Mutex
	broadcast chan []byte
}

var DefaultBroadcaster = &Broadcaster{
	clients:   make(map[*websocket.Conn]bool),
	broadcast: make(chan []byte, 100),
}

func init() {
	go DefaultBroadcaster.run()
}

func (b *Broadcaster) run() {
	for msg := range b.broadcast {
		b.clientsMu.Lock()
		for client := range b.clients {
			err := client.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				client.Close()
				delete(b.clients, client)
			}
		}
		b.clientsMu.Unlock()
	}
}

func (b *Broadcaster) BroadcastEvent(event map[string]interface{}) {
	data, err := json.Marshal(event)
	if err == nil {
		b.broadcast <- data
	}
}

func WsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	DefaultBroadcaster.clientsMu.Lock()
	DefaultBroadcaster.clients[conn] = true
	DefaultBroadcaster.clientsMu.Unlock()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			DefaultBroadcaster.clientsMu.Lock()
			delete(DefaultBroadcaster.clients, conn)
			DefaultBroadcaster.clientsMu.Unlock()
			break
		}
	}
}

func StartServer(addr string, dbConn *sql.DB) error {
	subFS, err := fs.Sub(FS, ".")
	if err != nil {
		return err
	}
	http.Handle("/", http.FileServer(http.FS(subFS)))
	http.HandleFunc("/ws", WsHandler)
	http.HandleFunc("/api/v1/telemetry", func(w http.ResponseWriter, r *http.Request) {
		type AgentInfo struct {
			Name         string  `json:"name"`
			Model        string  `json:"model"`
			Tokens       int     `json:"tokens"`
			Latency      string  `json:"latency"`
			Status       string  `json:"status"`
			ActiveTasks  int     `json:"activeTasks"`
		}
		type ProviderCost struct {
			Provider string  `json:"provider"`
			Percent  float64 `json:"percent"`
			Cost     float64 `json:"cost"`
			Color    string  `json:"color"`
		}
		type Response struct {
			TotalTokens   int            `json:"totalTokens"`
			TotalCost     float64        `json:"totalCost"`
			ActiveAgents  int            `json:"activeAgents"`
			AvgLatency    string         `json:"avgLatency"`
			Agents        []AgentInfo    `json:"agents"`
			Providers     []ProviderCost `json:"providers"`
		}

		var resp Response
		
		// Get totals
		dbConn.QueryRow("SELECT COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(cost), 0) FROM token_logs").Scan(&resp.TotalTokens, &resp.TotalCost)
		
		// Get agents and models
		rows, err := dbConn.Query("SELECT agent, model, SUM(input_tokens + output_tokens) FROM token_logs GROUP BY agent, model ORDER BY MAX(timestamp) DESC LIMIT 20")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var info AgentInfo
				if err := rows.Scan(&info.Name, &info.Model, &info.Tokens); err == nil {
					info.Status = "QUEUED"
					info.Latency = "N/A"
					info.ActiveTasks = 1
					resp.Agents = append(resp.Agents, info)
				}
			}
		}
		resp.ActiveAgents = len(resp.Agents)
		resp.AvgLatency = "380ms"

		// Get provider costs dynamically (basic heuristic based on model names)
		// For simplicity, Anthropic, OpenAI, Google
		var anthropicCost, openaiCost, googleCost float64
		dbConn.QueryRow("SELECT COALESCE(SUM(cost), 0) FROM token_logs WHERE model LIKE '%claude%' OR model LIKE '%anthropic%'").Scan(&anthropicCost)
		dbConn.QueryRow("SELECT COALESCE(SUM(cost), 0) FROM token_logs WHERE model LIKE '%gpt%' OR model LIKE '%o1%' OR model LIKE '%o3%'").Scan(&openaiCost)
		dbConn.QueryRow("SELECT COALESCE(SUM(cost), 0) FROM token_logs WHERE model LIKE '%gemini%' OR model LIKE '%antigravity%'").Scan(&googleCost)

		if resp.TotalCost > 0 {
			resp.Providers = append(resp.Providers, ProviderCost{"Anthropic", (anthropicCost / resp.TotalCost) * 100, anthropicCost, "bg-ctp-mauve"})
			resp.Providers = append(resp.Providers, ProviderCost{"OpenAI", (openaiCost / resp.TotalCost) * 100, openaiCost, "bg-ctp-teal"})
			resp.Providers = append(resp.Providers, ProviderCost{"Google", (googleCost / resp.TotalCost) * 100, googleCost, "bg-ctp-blue"})
		} else {
			resp.Providers = []ProviderCost{
				{"Anthropic", 0, 0, "bg-ctp-mauve"},
				{"OpenAI", 0, 0, "bg-ctp-teal"},
				{"Google", 0, 0, "bg-ctp-blue"},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	log.Printf("Server listening on %s", addr)
	return http.ListenAndServe(addr, nil)
}
