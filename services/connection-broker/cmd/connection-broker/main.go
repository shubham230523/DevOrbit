package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"devorbit.net/services/connection-broker/pkg/broker"
	"devorbit.net/services/connection-broker/pkg/observability"
)

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// statusRecorder captures the response status so operational counters can be derived without
// changing broker handler internals.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// countingHandler increments a rejection counter for authentication/authorization failures.
func countingHandler(next http.HandlerFunc, metrics *observability.Metrics, logger *observability.Logger, route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(recorder, r)
		switch recorder.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			metrics.Inc("devorbit_broker_auth_rejections_total")
			logger.SecurityEvent(observability.LevelWarn, "broker_auth_rejected", map[string]any{
				"route":  route,
				"method": r.Method,
				"status": recorder.status,
			})
		case http.StatusBadRequest:
			metrics.Inc("devorbit_broker_bad_requests_total")
		}
	}
}

func main() {
	env := strings.ToLower(getEnvOrDefault("DEVORBIT_ENV", "development"))
	brokerSecret := os.Getenv("DEVORBIT_BROKER_SECRET")

	if env == "production" {
		if brokerSecret == "" || len(brokerSecret) < 16 {
			log.Fatalf("Connection Broker Configuration Error: DEVORBIT_BROKER_SECRET is required and must be at least 16 characters in production mode")
		}
	}

	// Structured logging and metrics. Both are nil-safe and never block broker operation.
	logger := observability.NewLogger(os.Stderr, "connection-broker", "1.2.1", env, "info")
	metrics := observability.NewMetrics()
	startedAt := time.Now()

	b := broker.NewBroker()
	// Persisted authorization state is a convenience, not a security boundary: the broker keeps
	// running, and readiness/health remain the authoritative operational signal.
	b.LoadPersistedSessions()
	logger.OperationalEvent(observability.LevelInfo, "broker_state_loaded", map[string]any{
		"active_sessions": b.ActiveSessionCount(),
	})

	metrics.SetGaugeFunc("devorbit_broker_active_sessions", func() float64 {
		return float64(b.ActiveSessionCount())
	})
	metrics.SetGaugeFunc("devorbit_broker_uptime_seconds", func() float64 {
		return time.Since(startedAt).Seconds()
	})

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"connection-broker","env":"%s","active_sessions":%d}`, env, b.ActiveSessionCount())
	})

	// Liveness: process is up. No dependency checks, so an orchestrator does not restart the broker
	// because a downstream component is unhealthy.
	mux.HandleFunc("/api/v1/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"live","ts":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	})

	// Readiness: only ready when the service-to-service credential required to authorize sessions is
	// present. In production the process exits earlier if it is missing, so this is a defence in depth.
	mux.HandleFunc("/api/v1/health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if b.IsProduction() && brokerSecret == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"not_ready","reason":"broker_secret_not_configured"}`)
			return
		}
		fmt.Fprintf(w, `{"status":"ready","ts":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	})

	// Metrics are admin-protected: an unauthenticated administrative endpoint must not exist.
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if !b.AuthorizeAdmin(r) {
			metrics.Inc("devorbit_broker_auth_rejections_total")
			logger.SecurityEvent(observability.LevelWarn, "broker_auth_rejected", map[string]any{
				"route": "/metrics", "method": r.Method, "status": http.StatusUnauthorized,
			})
			http.Error(w, "Unauthorized: invalid service-to-service credentials", http.StatusUnauthorized)
			return
		}
		metrics.Handler().ServeHTTP(w, r)
	})

	mux.HandleFunc("/ws/host", countingHandler(b.HandleHostWebSocket, metrics, logger, "/ws/host"))
	mux.HandleFunc("/ws/client", countingHandler(b.HandleClientWebSocket, metrics, logger, "/ws/client"))
	mux.HandleFunc("/api/v1/internal/sessions/authorize", countingHandler(b.HandleAuthorizeSession, metrics, logger, "/api/v1/internal/sessions/authorize"))
	mux.HandleFunc("/api/v1/internal/devices/revoke", countingHandler(b.HandleRevoke, metrics, logger, "/api/v1/internal/devices/revoke"))

	port := getEnvOrDefault("DEVORBIT_BROKER_PORT", "8081")
	logger.OperationalEvent(observability.LevelInfo, "broker_started", map[string]any{
		"port":             port,
		"environment":      env,
		"active_sessions":  b.ActiveSessionCount(),
		"admin_configured": brokerSecret != "",
	})
	log.Printf("DevOrbit Connection Broker listening on :%s (Env: %s, Active Sessions: %d)", port, env, b.ActiveSessionCount())

	srv := &http.Server{Addr: ":" + port, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.OperationalEvent(observability.LevelError, "broker_stopped", map[string]any{"detail": err})
		log.Fatalf("Connection Broker server failed: %v", err)
	}
}
