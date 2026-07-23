package server

import (
	"fmt"
	"net/http"
)

func StartServer(port string) error {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "AI Tracker Dashboard. Serving /web eventually.")
	})

	fmt.Printf("Starting HTTP server on :%s\n", port)
	return http.ListenAndServe(":"+port, nil)
}
