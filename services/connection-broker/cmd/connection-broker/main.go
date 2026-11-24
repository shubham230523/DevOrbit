package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"devorbit.net/services/connection-broker/internal/broker"
)

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	env := strings.ToLower(getEnvOrDefault("DEVORBIT_ENV", "development"))
	brokerSecret := os.Getenv("DEVORBIT_BROKER_SECRET")

	if env == "production" {
		if brokerSecret == "" || len(brokerSecret) < 16 {
			log.Fatalf("Connection Broker Configuration Error: DEVORBIT_BROKER_SECRET is required and must be at least 16 characters in production mode")
		}
	}

	b := broker.NewBroker()
	b.LoadPersistedSessions()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"connection-broker","env":"%s","active_sessions":%d}`, env, b.ActiveSessionCount())
	})

	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)
	mux.HandleFunc("/api/v1/internal/sessions/authorize", b.HandleAuthorizeSession)
	mux.HandleFunc("/api/v1/internal/devices/revoke", b.HandleRevoke)

	port := getEnvOrDefault("DEVORBIT_BROKER_PORT", "8081")
	log.Printf("DevOrbit Connection Broker listening on :%s (Env: %s, Active Sessions: %d)", port, env, b.ActiveSessionCount())
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Connection Broker server failed: %v", err)
	}
}
