package main

import (
	"fmt"
	"log"
	"net/http"

	"devorbit.net/services/connection-broker/internal/broker"
)

func main() {
	b := broker.NewBroker()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"connection-broker","active_sessions":%d}`, b.ActiveSessionCount())
	})

	log.Printf("DevOrbit Connection Broker listening on :8081 (Active Sessions: %d)", b.ActiveSessionCount())
	if err := http.ListenAndServe(":8081", mux); err != nil {
		log.Fatalf("Connection Broker server failed: %v", err)
	}
}
