package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

type SessionProvisioner func(sessionID, deviceID, masterSecret string, expiresAt time.Time) error

type BrokerAuthorizer interface {
	AuthorizeSession(ctx context.Context, machineID, deviceID, sessionID, clientToken, hostToken string, expiresAt time.Time) error
	RevokeDevice(ctx context.Context, machineID, deviceID string) error
}

type Server struct {
	workspaceSvc       *application.WorkspaceService
	filesystemSvc      *application.FilesystemService
	processSvc         *application.ProcessService
	gitSvc             *application.GitService
	deviceSvc          *application.DeviceService
	pairingEngine      *security.PairingEngine
	rateLimiter        *security.RateLimiter
	idempotency        *security.IdempotencyManager
	brokerAuthorizer   BrokerAuthorizer
	sessionProvisioner SessionProvisioner
	agentEngine        *application.GeminiAgentEngine
	advertisedURL      string
	brokerHTTPURL      string
	brokerWSURL        string
	brokerURL          string
	hostToken          string
	trustedProxies     []string
	isProduction       bool
	aiRouter           *router.AIRouter
	muxOnce            sync.Once
	cachedMux          *http.ServeMux
}

func NewServer(
	ws *application.WorkspaceService,
	fs *application.FilesystemService,
	ps *application.ProcessService,
	gs *application.GitService,
	ds *application.DeviceService,
) *Server {
	return &Server{
		workspaceSvc:  ws,
		filesystemSvc: fs,
		processSvc:    ps,
		gitSvc:        gs,
		deviceSvc:     ds,
	}
}

func (s *Server) SetBrokerAuthorizer(authorizer BrokerAuthorizer) {
	s.brokerAuthorizer = authorizer
}

func (s *Server) SetSessionProvisioner(provisioner SessionProvisioner) {
	s.sessionProvisioner = provisioner
}

func (s *Server) SetAgentEngine(engine *application.GeminiAgentEngine) {
	s.agentEngine = engine
}

func (s *Server) SetAIRouter(aiRouter *router.AIRouter) {
	s.aiRouter = aiRouter
}

func (s *Server) SetAdvertisedURL(url string) {
	s.advertisedURL = url
}

func (s *Server) SetBrokerURL(url string) {
	s.brokerURL = url
}

func (s *Server) SetBrokerEndpoints(httpURL, wsURL, hostToken string) {
	s.brokerHTTPURL = httpURL
	s.brokerWSURL = wsURL
	s.hostToken = hostToken
}

func (s *Server) SetTrustedProxies(proxies []string) {
	s.trustedProxies = proxies
}

func (s *Server) SetIsProduction(isProd bool) {
	s.isProduction = isProd
}

func (s *Server) Mux() *http.ServeMux {
	s.muxOnce.Do(func() {
		s.cachedMux = http.NewServeMux()
		s.RegisterRoutes(s.cachedMux)
	})
	return s.cachedMux
}

func (s *Server) SetIdempotencyManager(mgr *security.IdempotencyManager) {
	s.idempotency = mgr
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	wsList, _ := s.workspaceSvc.ListWorkspaces()
	hostname, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":                   "ok",
		"os":                       runtime.GOOS,
		"arch":                     runtime.GOARCH,
		"hostname":                 hostname,
		"host_bridge_version":      "1.2.1",
		"allowed_workspaces_count": len(wsList),
	})
}

func (s *Server) HandleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces, err := s.workspaceSvc.ListWorkspaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workspaces)
}

func (s *Server) HandleDiscoverWorkspaces(w http.ResponseWriter, r *http.Request) {
	var opts domain.DiscoverOptions
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			writeError(w, http.StatusBadRequest, "invalid discovery options JSON")
			return
		}
	}
	discovered, err := s.workspaceSvc.DiscoverWorkspaces(r.Context(), &opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, discovered)
}

func (s *Server) HandleListDirectory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID  string `json:"workspace_id"`
		RelativePath string `json:"relative_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid list directory JSON")
		return
	}

	listing, err := s.filesystemSvc.ListDirectory(req.WorkspaceID, req.RelativePath)
	if err != nil {
		if errors.Is(err, domain.ErrAccessDenied) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusNotFound, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Server) HandleReadFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID  string `json:"workspace_id"`
		RelativePath string `json:"relative_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid read file JSON")
		return
	}

	res, err := s.filesystemSvc.ReadFile(req.WorkspaceID, req.RelativePath)
	if err != nil {
		if errors.Is(err, domain.ErrAccessDenied) {
			writeError(w, http.StatusForbidden, err.Error())
		} else if errors.Is(err, domain.ErrFileTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		} else {
			writeError(w, http.StatusNotFound, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) HandleWriteFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		domain.WriteFileRequest
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid write file JSON")
		return
	}

	if s.idempotency != nil && req.RequestID != "" {
		if cached, found := s.idempotency.Get(req.RequestID); found {
			writeJSON(w, http.StatusOK, cached)
			return
		}
	}

	res, err := s.filesystemSvc.WriteFile(&req.WriteFileRequest)
	if err != nil {
		var conflictErr *domain.ConflictError
		if errors.As(err, &conflictErr) {
			writeJSON(w, http.StatusConflict, conflictErr)
			return
		}
		if errors.Is(err, domain.ErrAccessDenied) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if s.idempotency != nil && req.RequestID != "" {
		s.idempotency.Store(req.RequestID, res)
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) HandleGitStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request JSON")
		return
	}

	st, err := s.gitSvc.GetStatus(r.Context(), req.WorkspaceID)
	if err != nil {
		if errors.Is(err, domain.ErrAccessDenied) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) HandleGitDiff(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request JSON")
		return
	}

	diff, err := s.gitSvc.GetDiff(r.Context(), req.WorkspaceID)
	if err != nil {
		if errors.Is(err, domain.ErrAccessDenied) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) HandleExecuteProcess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		domain.ProcessSpec
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid process spec JSON")
		return
	}

	if s.idempotency != nil && req.RequestID != "" {
		if cached, found := s.idempotency.Get(req.RequestID); found {
			writeJSON(w, http.StatusOK, cached)
			return
		}
	}

	info, err := s.processSvc.ExecuteProcess(r.Context(), &req.ProcessSpec)
	if err != nil {
		if errors.Is(err, domain.ErrCommandNotAllowed) {
			writeError(w, http.StatusForbidden, err.Error())
		} else if errors.Is(err, domain.ErrAccessDenied) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if s.idempotency != nil && req.RequestID != "" {
		s.idempotency.Store(req.RequestID, info)
	}

	writeJSON(w, http.StatusOK, info)
}

func (s *Server) HandleProcessById(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/processes/")
	if strings.HasSuffix(id, "/cancel") {
		id = strings.TrimSuffix(id, "/cancel")
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ok := s.processSvc.CancelProcess(id)
		if !ok {
			writeError(w, http.StatusNotFound, "process not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
		return
	}

	info, ok := s.processSvc.GetProcessStatus(id)
	if !ok {
		writeError(w, http.StatusNotFound, "process not found")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) HandleListADBDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.deviceSvc.ListADBDevices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, []domain.ADBDevice{})
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *Server) HandleAIPrompt(w http.ResponseWriter, r *http.Request) {
	if s.aiRouter == nil {
		writeError(w, http.StatusServiceUnavailable, "AI router service not configured")
		return
	}

	var req router.GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request JSON")
		return
	}

	if req.Model == "" {
		req.Model = "gemini-2.5-flash"
	}

	resp, err := s.aiRouter.Generate(r.Context(), req)
	if err != nil {
		if errors.Is(err, router.ErrProviderUnavailable) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) HandleCreateAgentTask(w http.ResponseWriter, r *http.Request) {
	if s.agentEngine == nil {
		writeError(w, http.StatusInternalServerError, "agent engine not initialized")
		return
	}

	var req struct {
		WorkspaceID string `json:"workspace_id"`
		Prompt      string `json:"prompt"`
		ModelID     string `json:"model_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request JSON")
		return
	}

	if req.WorkspaceID == "" || req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "workspace_id and prompt are required")
		return
	}

	task, err := s.agentEngine.CreateTask(req.WorkspaceID, req.Prompt, req.ModelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	go func() {
		_ = s.agentEngine.RunTask(context.Background(), task.ID, "")
	}()

	writeJSON(w, http.StatusOK, task)
}

func (s *Server) HandleApproveAgentTask(w http.ResponseWriter, r *http.Request) {
	if s.agentEngine == nil {
		writeError(w, http.StatusInternalServerError, "agent engine not initialized")
		return
	}

	var req struct {
		TaskID        string `json:"task_id"`
		ApprovalToken string `json:"approval_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == "" || req.ApprovalToken == "" {
		writeError(w, http.StatusBadRequest, "task_id and approval_token are required")
		return
	}

	go func() {
		_ = s.agentEngine.ResumeTaskWithApproval(context.Background(), req.TaskID, req.ApprovalToken)
	}()

	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) HandleGetAgentTask(w http.ResponseWriter, r *http.Request) {
	if s.agentEngine == nil {
		writeError(w, http.StatusInternalServerError, "agent engine not initialized")
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/api/v1/agent/tasks/")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}

	task, found := s.agentEngine.GetTask(taskID)
	if !found {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (s *Server) HandleCancelAgentTask(w http.ResponseWriter, r *http.Request) {
	if s.agentEngine == nil {
		writeError(w, http.StatusInternalServerError, "agent engine not initialized")
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}

	ok := s.agentEngine.CancelTask(req.TaskID)
	writeJSON(w, http.StatusOK, map[string]any{"task_id": req.TaskID, "cancelled": ok})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
