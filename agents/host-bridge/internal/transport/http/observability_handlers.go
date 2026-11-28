package http

import (
	"errors"
	"net/http"
	"time"

	"devorbit.net/agents/host-bridge/internal/security"
	"devorbit.net/services/connection-broker/pkg/observability"
)

// SecurityEventNames used by the Host Bridge. Centralised so logs and tests cannot drift.
const (
	EventPairingSucceeded     = "pairing_succeeded"
	EventPairingFailed        = "pairing_failed"
	EventAuthenticationFailed = "authentication_failed"
	EventAuthorizationDenied  = "authorization_denied"
	EventSessionRevoked       = "session_revoked"
	EventRevocationFailed     = "revocation_failed"
	EventRateLimited          = "rate_limited"
	EventCommandPolicyReject  = "command_policy_rejected"
	EventApprovalRequested    = "approval_requested"
	EventApprovalDecided      = "approval_decided"
	EventTransportFailure     = "transport_failure"
	EventStartup              = "host_bridge_started"
	EventShutdown             = "host_bridge_stopping"
	EventHealthReady          = "host_bridge_ready"
	// Process approval lifecycle. Tokens are never part of any event field.
	EventProcessApprovalRequested       = "process_approval_requested"
	EventProcessApprovalApproved        = "process_approval_approved"
	EventProcessApprovalRejected        = "process_approval_failed"
	EventProcessApprovalRejectedDecided = "process_approval_rejected_by_user"
	EventProcessApproved                = "process_approval_consumed"
)

// SetObservability installs the structured logger and metrics registry. Both are optional and
// nil-safe: a Server without observability keeps working exactly as before.
func (s *Server) SetObservability(logger *observability.Logger, metrics *observability.Metrics) {
	s.logger = logger
	s.metrics = metrics
}

// SetReady marks the instance ready to serve authenticated traffic. Readiness must only be set once
// required security initialisation (secret store, session persistence, broker client) succeeded.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// IsReady reports whether security initialisation completed.
func (s *Server) IsReady() bool {
	return s.ready.Load()
}

// securityEvent emits a structured security event when observability is configured.
func (s *Server) securityEvent(level, event string, fields map[string]any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.SecurityEvent(level, event, fields)
}

// operationalEvent emits a structured operational event when observability is configured.
func (s *Server) operationalEvent(level, event string, fields map[string]any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.OperationalEvent(level, event, fields)
}

// incrementMetric increments a counter when metrics are configured.
func (s *Server) incrementMetric(name string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.metrics.Inc(name)
}

// revokedScope reports whether a revocation was device-wide or session-only, without logging the
// identifier itself.
func revokedScope(deviceID, sessionID string) string {
	switch {
	case deviceID != "":
		return "device"
	case sessionID != "":
		return "session"
	default:
		return "none"
	}
}

// denySecurity records an authorization/authentication denial and writes the response. Reasons are
// stable codes rather than raw credentials or caller input.
func (s *Server) denySecurity(w http.ResponseWriter, r *http.Request, reason string, status int, message string) {
	fields := map[string]any{
		"reason": reason,
		"method": r.Method,
		"route":  r.URL.Path,
	}
	if r != nil {
		fields["client_ip"] = ExtractIP(r.RemoteAddr)
	}
	s.securityEvent(observability.LevelWarn, EventAuthorizationDenied, fields)
	s.incrementMetric("devorbit_authorization_denied_total")
	writeError(w, status, message)
}

// pairingFailureReason maps a pairing error to a stable, non-sensitive reason code for logs and
// metrics. Raw error text is never used as a reason because it may embed caller-supplied values.
func pairingFailureReason(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, security.ErrReplayDetected):
		return "replay_detected"
	case errors.Is(err, security.ErrPairingExpired):
		return "challenge_expired"
	case errors.Is(err, security.ErrPairingUsed):
		return "challenge_already_used"
	case errors.Is(err, security.ErrInvalidProof):
		return "invalid_proof"
	case errors.Is(err, security.ErrInvalidTimestamp):
		return "invalid_timestamp"
	default:
		return "internal_error"
	}
}

// HandleHealthLive reports process liveness only. It performs no dependency checks so a failed
// dependency does not cause an orchestrator to kill an otherwise recoverable process.
func (s *Server) HandleHealthLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "live",
		"ts":     time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleHealthReady reports readiness. It returns 503 until security initialisation has completed
// (secret store availability resolved and session persistence loaded), so a partially initialised
// instance is never advertised as ready.
func (s *Server) HandleHealthReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.IsReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "not_ready",
			"reason": "security initialization incomplete",
			"ts":     time.Now().UTC().Format(time.RFC3339),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ready",
		"ts":     time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleMetrics exposes Prometheus text exposition. It is host-admin protected because metric
// names and values are operational metadata; production must not expose an unauthenticated
// administrative endpoint.
func (s *Server) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.Error(w, "metrics not enabled", http.StatusNotFound)
		return
	}
	s.metrics.Handler().ServeHTTP(w, r)
}
