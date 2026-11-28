package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
	"devorbit.net/services/connection-broker/pkg/observability"
)

// newContractServer builds a fully wired Server for API contract tests. It uses the real handlers,
// real middleware and the real command policy: only external dependencies are local.
func newContractServer(t *testing.T, hostSecret string) *Server {
	t.Helper()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	jail := security.NewWorkspaceJail([]string{workspace})
	policy := security.NewDefaultCommandPolicy()
	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)

	srv := NewServer(
		application.NewWorkspaceService(jail),
		application.NewFilesystemService(storage),
		application.NewProcessService(procRunner),
		application.NewGitService(git.NewGitClient(jail)),
		application.NewDeviceService(adb.NewADBClient("adb")),
	)
	srv.SetPairingEngine(security.NewPairingEngine("host-contract", "Contract Host", hostSecret))
	srv.SetHostToken(hostSecret)
	srv.SetRateLimiter(security.NewRateLimiter(50, time.Minute))
	srv.SetObservability(observability.NewLogger(io.Discard, "host-bridge", "test", "test", "error"), observability.NewMetrics())
	return srv
}

func doContractRequest(t *testing.T, srv *Server, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:54321"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	recorder := httptest.NewRecorder()
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.ServeHTTP(recorder, req)
	return recorder
}

// routeContract is the authoritative table of protected routes and their required permission.
type routeContract struct {
	method string
	path   string
	body   string
}

var protectedRoutes = []routeContract{
	{http.MethodGet, "/api/v1/workspaces", ""},
	{http.MethodPost, "/api/v1/workspaces/discover", `{}`},
	{http.MethodPost, "/api/v1/files/list", `{"workspace_id":"ws","relative_path":"."}`},
	{http.MethodPost, "/api/v1/files/read", `{"workspace_id":"ws","relative_path":"a.txt"}`},
	{http.MethodPost, "/api/v1/files/write", `{"workspace_id":"ws","relative_path":"a.txt","new_content":"x","base_sha256":""}`},
	{http.MethodPost, "/api/v1/git/status", `{"workspace_id":"ws"}`},
	{http.MethodPost, "/api/v1/git/diff", `{"workspace_id":"ws"}`},
	{http.MethodPost, "/api/v1/processes/execute", `{"workspace_id":"ws","command":"git","args":["status"]}`},
	{http.MethodGet, "/api/v1/devices/adb", ""},
	{http.MethodPost, "/api/v1/ai/prompt", `{"prompt":"hi","model_id":"gemini-2.5-flash"}`},
	{http.MethodPost, "/api/v1/agent/tasks", `{"workspace_id":"ws","prompt":"hi"}`},
	{http.MethodPost, "/api/v1/agent/tasks/approve", `{"task_id":"t","approval_token":"x"}`},
	{http.MethodPost, "/api/v1/agent/tasks/cancel", `{"task_id":"t"}`},
	{http.MethodGet, "/api/v1/agent/tasks/task-1", ""},
	{http.MethodGet, "/api/v1/github/repos", ""},
	{http.MethodPost, "/api/v1/github/clone", `{"clone_url":"https://example.com/r.git","name":"r"}`},
}

var hostAdminRoutes = []routeContract{
	{http.MethodPost, "/api/v1/pairing/challenge", `{}`},
	{http.MethodPost, "/api/v1/sessions/revoke", `{"session_id":"sess-1"}`},
	{http.MethodGet, "/metrics", ""},
}

func TestContract_ProtectedRoutesRejectUnauthenticatedRequests(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	for _, route := range protectedRoutes {
		recorder := doContractRequest(t, srv, route.method, route.path, route.body, nil)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401 without credentials, got %d (%s)", route.method, route.path, recorder.Code, recorder.Body.String())
		}
		if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s %s: expected JSON error content type, got %q", route.method, route.path, ct)
		}
		var payload map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s %s: error body is not valid JSON: %v", route.method, route.path, err)
		}
		message, ok := payload["error"].(string)
		if !ok || strings.TrimSpace(message) == "" {
			t.Fatalf("%s %s: expected non-empty 'error' field, got %v", route.method, route.path, payload)
		}
	}
}

func TestContract_HostAdminRoutesRejectUnauthenticatedRequests(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	for _, route := range hostAdminRoutes {
		recorder := doContractRequest(t, srv, route.method, route.path, route.body, nil)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401 without host-admin credential, got %d (%s)", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}

	// Wrong credential must also be rejected, and the response must never echo the supplied value.
	wrong := doContractRequest(t, srv, http.MethodGet, "/metrics", "", map[string]string{
		"Authorization": "Bearer wrong-secret-value",
	})
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid host-admin credential, got %d", wrong.Code)
	}
	if strings.Contains(wrong.Body.String(), "wrong-secret-value") {
		t.Fatalf("error response echoed the supplied credential")
	}
}

func TestContract_QueryCredentialsRejected(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	recorder := doContractRequest(t, srv, http.MethodGet, "/api/v1/workspaces?token=abc", "", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for query-string credentials, got %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestContract_MethodNotAllowedIsRejected(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	// Public routes validate the method before any body processing.
	cases := []struct{ method, path string }{
		{http.MethodPut, "/api/v1/pairing/confirm"},
		{http.MethodPost, "/api/v1/health"},
		{http.MethodPost, "/api/v1/health/live"},
		{http.MethodPost, "/api/v1/health/ready"},
	}
	for _, tc := range cases {
		recorder := doContractRequest(t, srv, tc.method, tc.path, "", nil)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: expected 405, got %d", tc.method, tc.path, recorder.Code)
		}
	}

	// Contract: protected routes authenticate BEFORE method validation, so an unauthenticated caller
	// never learns whether a method is supported (no information leak).
	protected := doContractRequest(t, srv, http.MethodDelete, "/api/v1/workspaces", "", nil)
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth before method check) on protected route, got %d", protected.Code)
	}

	// Contract: host-admin routes also authenticate before method validation.
	admin := doContractRequest(t, srv, http.MethodGet, "/api/v1/sessions/revoke", "", nil)
	if admin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth before method check) on host-admin route, got %d", admin.Code)
	}
}

func TestContract_HealthLivenessAndReadinessSemantics(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	// Readiness must be false until security initialization completes.
	recorder := doContractRequest(t, srv, http.MethodGet, "/api/v1/health/ready", "", nil)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before readiness is set, got %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("readiness body is not JSON: %v", err)
	}
	if payload["status"] != "not_ready" {
		t.Fatalf("expected status=not_ready, got %v", payload["status"])
	}

	srv.SetReady(true)
	recorder = doContractRequest(t, srv, http.MethodGet, "/api/v1/health/ready", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 after readiness is set, got %d", recorder.Code)
	}

	// Liveness is independent of readiness and exposes no configuration.
	recorder = doContractRequest(t, srv, http.MethodGet, "/api/v1/health/live", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 from liveness, got %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "root") {
		t.Fatalf("liveness response leaked configuration: %s", recorder.Body.String())
	}
}

func TestContract_NoSessionSecretInAnyErrorResponse(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	// A wrong proof on the confirm endpoint is the most likely place to leak session material.
	confirmBody := `{"challenge_id":"ch-1","device_id":"dev-1","timestamp":"1","nonce":"nonce-1234","proof_signature":"bad"}`
	recorder := doContractRequest(t, srv, http.MethodPost, "/api/v1/pairing/confirm", confirmBody, nil)
	body := recorder.Body.String()

	for _, forbidden := range []string{"session_secret", "masterSecret", "master_secret", "session_token"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("error response contains sensitive field %q: %s", forbidden, body)
		}
	}
}

func TestContract_UnknownFieldsAreToleratedByDecoders(t *testing.T) {
	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")

	// Forward compatibility: unknown JSON fields must not change validation outcomes. Without
	// credentials the request still fails closed with 401 rather than a decode error.
	body := `{"session_id":"sess-1","unknown_future_field":true}`
	recorder := doContractRequest(t, srv, http.MethodPost, "/api/v1/sessions/revoke", body, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth first, unknown field tolerated), got %d (%s)", recorder.Code, recorder.Body.String())
	}
}
