package http

import (
	"net/http"
	"strings"

	"devorbit.net/agents/host-bridge/internal/security"
)

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Public minimal health endpoint (returns full metadata only when authenticated)
	mux.HandleFunc("/api/v1/health", s.HandleHealth)

	// Liveness/readiness endpoints for orchestrators. Neither exposes configuration or secrets:
	// readiness reports 503 until security initialisation has completed.
	mux.HandleFunc("/api/v1/health/live", s.HandleHealthLive)
	mux.HandleFunc("/api/v1/health/ready", s.HandleHealthReady)

	// Operational metrics are host-admin protected: production must never expose an
	// unauthenticated administrative surface.
	mux.HandleFunc("/metrics", s.withHostAdmin(http.MethodGet, s.HandleMetrics))

	// Host-admin-only pairing challenge generation (returns ephemeral pairing_secret)
	mux.HandleFunc("/api/v1/pairing/challenge", s.withHostAdmin(http.MethodPost, s.HandleCreatePairingChallenge))

	// Public pairing confirmation endpoint protected by ephemeral HMAC proof-of-possession and IP rate limiter
	mux.HandleFunc("/api/v1/pairing/confirm", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleConfirmPairing(w, r)
	})

	// Host-admin-only session/device revocation endpoint
	mux.HandleFunc("/api/v1/sessions/revoke", s.withHostAdmin(http.MethodPost, s.HandleRevokeSession))

	// Authenticated E2E AES-256-GCM HTTP tunnel endpoint for direct LAN and HTTPS clients
	mux.HandleFunc("/api/v1/e2e", s.HandleE2ERequest)

	// Protected workspace endpoints
	mux.HandleFunc("/api/v1/workspaces", s.withAuth(http.MethodGet, security.PermissionWorkspaceList, s.HandleListWorkspaces))
	mux.HandleFunc("/api/v1/workspaces/discover", s.withAuth(http.MethodPost, security.PermissionWorkspaceDiscover, s.HandleDiscoverWorkspaces))

	// Protected filesystem endpoints
	mux.HandleFunc("/api/v1/files/list", s.withAuth(http.MethodPost, security.PermissionFileRead, s.HandleListDirectory))
	mux.HandleFunc("/api/v1/files/read", s.withAuth(http.MethodPost, security.PermissionFileRead, s.HandleReadFile))
	mux.HandleFunc("/api/v1/files/write", s.withAuth(http.MethodPost, security.PermissionFileWrite, s.HandleWriteFile))

	// Protected Git endpoints
	mux.HandleFunc("/api/v1/git/status", s.withAuth(http.MethodPost, security.PermissionGitInspect, s.HandleGitStatus))
	mux.HandleFunc("/api/v1/git/diff", s.withAuth(http.MethodPost, security.PermissionGitInspect, s.HandleGitDiff))

	// Protected process endpoints
	mux.HandleFunc("/api/v1/processes/execute", s.withAuth(http.MethodPost, security.PermissionProcessExecute, s.HandleExecuteProcess))
	// Approval issuance for commands that execute project-defined code (Gradle family).
	mux.HandleFunc("/api/v1/processes/approvals", s.withAuth(http.MethodPost, security.PermissionProcessExecute, s.HandleCreateProcessApproval))
	mux.HandleFunc("/api/v1/processes/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/processes/") {
			http.NotFound(w, r)
			return
		}
		s.HandleProcessById(w, r)
	})

	// Protected ADB device endpoint
	mux.HandleFunc("/api/v1/devices/adb", s.withAuth(http.MethodGet, security.PermissionDeviceInspect, s.HandleListADBDevices))

	// Protected AI & Agent endpoints
	mux.HandleFunc("/api/v1/ai/prompt", s.withAuth(http.MethodPost, security.PermissionAIExecute, s.HandleAIPrompt))
	mux.HandleFunc("/api/v1/agent/tasks", s.withAuth(http.MethodPost, security.PermissionAgentExecute, s.HandleCreateAgentTask))
	mux.HandleFunc("/api/v1/agent/tasks/approve", s.withAuth(http.MethodPost, security.PermissionAgentApprove, s.HandleApproveAgentTask))
	mux.HandleFunc("/api/v1/agent/tasks/cancel", s.withAuth(http.MethodPost, security.PermissionAgentExecute, s.HandleCancelAgentTask))
	mux.HandleFunc("/api/v1/agent/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/agent/tasks/") {
			http.NotFound(w, r)
			return
		}
		s.withAuth(http.MethodGet, security.PermissionAgentRead, s.HandleGetAgentTask)(w, r)
	})

	// Protected GitHub endpoints
	mux.HandleFunc("/api/v1/github/repos", s.withAuth(http.MethodGet, security.PermissionGitHubRead, s.HandleListGitHubRepos))
	mux.HandleFunc("/api/v1/github/clone", s.withAuth(http.MethodPost, security.PermissionGitHubClone, s.HandleCloneGitHubRepo))
}
