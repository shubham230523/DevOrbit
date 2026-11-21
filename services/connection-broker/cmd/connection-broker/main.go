package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"devorbit.net/services/connection-broker/internal/broker"
)

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	b := broker.NewBroker()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"connection-broker","active_sessions":%d}`, b.ActiveSessionCount())
	})

	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	port := getEnvOrDefault("DEVORBIT_BROKER_PORT", "8081")
	log.Printf("DevOrbit Connection Broker listening on :%s (Active Sessions: %d)", port, b.ActiveSessionCount())
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Connection Broker server failed: %v", err)
	}
}
