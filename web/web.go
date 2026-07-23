package web

import (
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

func StartServer(addr string) error {
	subFS, err := fs.Sub(FS, ".")
	if err != nil {
		return err
	}
	http.Handle("/", http.FileServer(http.FS(subFS)))
	http.HandleFunc("/ws", WsHandler)
	log.Printf("Server listening on %s", addr)
	return http.ListenAndServe(addr, nil)
}
