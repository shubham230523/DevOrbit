package http

import (
	"context"
	"crypto/subtle"
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
type SessionRevoker func(sessionID, deviceID string)

type BrokerAuthorizer interface {
	AuthorizeSession(ctx context.Context, machineID, deviceID, sessionID, clientToken, hostToken string, expiresAt time.Time) error
	RevokeDevice(ctx context.Context, machineID, deviceID string) error
}

type resourceOwner struct {
	SessionID string
	DeviceID  string
}

type cachedPairingSession struct {
	sessionToken  string
	sessionID     string
	sessionSecret string
	expiry        time.Time
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
	sessionStoreMu     sync.Mutex
	sessionStore       *security.SessionStore
	sessionRevoker     SessionRevoker
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
	githubAPIBaseURL   string
	githubCloneBaseDir string
	muxOnce            sync.Once
	cachedMux          *http.ServeMux
	processOwnersMu    sync.RWMutex
	processOwners      map[string]resourceOwner
	pairingCacheMu     sync.Mutex
	pairingCache       map[string]cachedPairingSession
}

func NewServer(
	ws *application.WorkspaceService,
	fs *application.FilesystemService,
	ps *application.ProcessService,
	gs *application.GitService,
	ds *application.DeviceService,
) *Server {
	s := &Server{
		workspaceSvc:  ws,
		filesystemSvc: fs,
		processSvc:    ps,
		gitSvc:        gs,
		deviceSvc:     ds,
		sessionStore:  security.NewSessionStore(),
		processOwners: make(map[string]resourceOwner),
	}
	s.wireSessionStoreHooks(s.sessionStore)
	return s
}

func (s *Server) wireSessionStoreHooks(store *security.SessionStore) {
	if store == nil {
		return
	}
	store.SetLifecycleHooks(
		func(sessionID string) {
			s.invalidatePairingRetryForSession(sessionID)
		},
		func(sessionID, _ string) {
			s.invalidatePairingRetryForSession(sessionID)
		},
	)
}

func (s *Server) invalidatePairingRetryForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	if s.pairingEngine != nil {
		s.pairingEngine.InvalidateSessionRetry(sessionID)
	}
	s.pairingCacheMu.Lock()
	for k, v := range s.pairingCache {
		if v.sessionID == sessionID {
			delete(s.pairingCache, k)
		}
	}
	s.pairingCacheMu.Unlock()
}

func (s *Server) SetSessionStore(store *security.SessionStore) {
	s.sessionStoreMu.Lock()
	defer s.sessionStoreMu.Unlock()
	s.sessionStore = store
	s.wireSessionStoreHooks(store)
}

func (s *Server) SessionStore() *security.SessionStore {
	return s.ensureSessionStore()
}

func (s *Server) SetSessionRevoker(revoker SessionRevoker) {
	s.sessionRevoker = revoker
}

func (s *Server) SetHostToken(hostToken string) {
	s.hostToken = hostToken
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
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if authCtx, ok := s.tryAuthenticateOptional(r); ok && authCtx != nil {
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
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func (s *Server) HandleRevokeSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHostAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		DeviceID  string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid revoke session JSON")
		return
	}
	if req.SessionID == "" && req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "session_id or device_id is required")
		return
	}

	store := s.ensureSessionStore()
	if req.SessionID != "" {
		store.RevokeSession(req.SessionID)
	}
	if req.DeviceID != "" {
		store.RevokeDevice(req.DeviceID)
	}
	if s.sessionRevoker != nil {
		s.sessionRevoker(req.SessionID, req.DeviceID)
	}
	if s.brokerAuthorizer != nil && req.DeviceID != "" && s.pairingEngine != nil {
		_ = s.brokerAuthorizer.RevokeDevice(r.Context(), s.pairingEngine.HostID(), req.DeviceID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) HandleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionWorkspaceList); !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workspaces, err := s.workspaceSvc.ListWorkspaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workspaces)
}

func (s *Server) HandleDiscoverWorkspaces(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionWorkspaceDiscover); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	if _, ok := s.authorizeRequest(w, r, security.PermissionFileRead); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	if _, ok := s.authorizeRequest(w, r, security.PermissionFileRead); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionFileWrite)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		domain.WriteFileRequest
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid write file JSON")
		return
	}

	idemKey := ""
	if req.RequestID != "" {
		idemKey = "write:" + authCtx.SessionID + ":" + authCtx.DeviceID + ":" + req.RequestID
	}
	if s.idempotency != nil && idemKey != "" {
		if cached, found := s.idempotency.Get(idemKey); found {
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

	if s.idempotency != nil && idemKey != "" {
		s.idempotency.Store(idemKey, res)
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) HandleGitStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionGitInspect); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	if _, ok := s.authorizeRequest(w, r, security.PermissionGitInspect); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

func (s *Server) recordProcessOwner(procID, sessionID, deviceID string) {
	if procID == "" {
		return
	}
	s.processOwnersMu.Lock()
	defer s.processOwnersMu.Unlock()
	if s.processOwners == nil {
		s.processOwners = make(map[string]resourceOwner)
	}
	s.processOwners[procID] = resourceOwner{SessionID: sessionID, DeviceID: deviceID}
}

func (s *Server) checkProcessOwnership(procID, sessionID, deviceID string) (known bool, owned bool) {
	s.processOwnersMu.RLock()
	defer s.processOwnersMu.RUnlock()
	owner, exists := s.processOwners[procID]
	if !exists {
		return false, false
	}
	return true, owner.SessionID == sessionID && owner.DeviceID == deviceID
}

func (s *Server) HandleExecuteProcess(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionProcessExecute)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		domain.ProcessSpec
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid process spec JSON")
		return
	}

	idemKey := ""
	if req.RequestID != "" {
		idemKey = "proc:" + authCtx.SessionID + ":" + authCtx.DeviceID + ":" + req.RequestID
	}
	if s.idempotency != nil && idemKey != "" {
		if cached, found := s.idempotency.Get(idemKey); found {
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

	s.recordProcessOwner(info.ID, authCtx.SessionID, authCtx.DeviceID)

	if s.idempotency != nil && idemKey != "" {
		s.idempotency.Store(idemKey, info)
	}

	writeJSON(w, http.StatusOK, info)
}

func (s *Server) HandleProcessById(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/processes/")
	if strings.HasSuffix(rest, "/cancel") {
		authCtx, ok := s.authorizeRequest(w, r, security.PermissionProcessExecute)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimSuffix(rest, "/cancel")
		if id == "" || strings.Contains(id, "/") {
			writeError(w, http.StatusBadRequest, "invalid process id")
			return
		}
		known, owned := s.checkProcessOwnership(id, authCtx.SessionID, authCtx.DeviceID)
		if known && !owned {
			writeError(w, http.StatusForbidden, "process belongs to a different session or device")
			return
		}
		if !known {
			writeError(w, http.StatusNotFound, "process not found")
			return
		}
		cancelled := s.processSvc.CancelProcess(id)
		if !cancelled {
			writeError(w, http.StatusNotFound, "process not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
		return
	}

	authCtx, ok := s.authorizeRequest(w, r, security.PermissionProcessRead)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if rest == "" || strings.Contains(rest, "/") {
		writeError(w, http.StatusBadRequest, "invalid process id")
		return
	}

	known, owned := s.checkProcessOwnership(rest, authCtx.SessionID, authCtx.DeviceID)
	if known && !owned {
		writeError(w, http.StatusForbidden, "process belongs to a different session or device")
		return
	}
	if !known {
		writeError(w, http.StatusNotFound, "process not found")
		return
	}

	info, found := s.processSvc.GetProcessStatus(rest)
	if !found {
		writeError(w, http.StatusNotFound, "process not found")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) HandleListADBDevices(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionDeviceInspect); !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	devices, err := s.deviceSvc.ListADBDevices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, []domain.ADBDevice{})
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *Server) HandleAIPrompt(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionAIExecute); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionAgentExecute)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

	task, err := s.agentEngine.CreateTaskForOwner(req.WorkspaceID, req.Prompt, req.ModelID, authCtx.SessionID, authCtx.DeviceID)
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
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionAgentApprove)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

	task, found := s.agentEngine.GetTask(req.TaskID)
	if !found {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if task.OwnerSessionID != authCtx.SessionID || task.OwnerDeviceID != authCtx.DeviceID {
		writeError(w, http.StatusForbidden, "task belongs to a different session or device")
		return
	}
	if task.Status != "approval_required" {
		writeError(w, http.StatusConflict, "task is not awaiting approval")
		return
	}
	if task.ApprovalToken == "" || subtle.ConstantTimeCompare([]byte(req.ApprovalToken), []byte(task.ApprovalToken)) != 1 {
		writeError(w, http.StatusForbidden, "invalid approval token")
		return
	}

	go func() {
		_ = s.agentEngine.ResumeTaskWithApproval(context.Background(), req.TaskID, req.ApprovalToken)
	}()

	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) HandleGetAgentTask(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionAgentRead)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.agentEngine == nil {
		writeError(w, http.StatusInternalServerError, "agent engine not initialized")
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/api/v1/agent/tasks/")
	if taskID == "" || strings.Contains(taskID, "/") {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}

	task, found := s.agentEngine.GetTask(taskID)
	if !found {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if task.OwnerSessionID != authCtx.SessionID || task.OwnerDeviceID != authCtx.DeviceID {
		writeError(w, http.StatusForbidden, "task belongs to a different session or device")
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (s *Server) HandleCancelAgentTask(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionAgentExecute)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
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

	task, found := s.agentEngine.GetTask(req.TaskID)
	if !found {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if task.OwnerSessionID != authCtx.SessionID || task.OwnerDeviceID != authCtx.DeviceID {
		writeError(w, http.StatusForbidden, "task belongs to a different session or device")
		return
	}

	cancelled := s.agentEngine.CancelTask(req.TaskID)
	writeJSON(w, http.StatusOK, map[string]any{"task_id": req.TaskID, "cancelled": cancelled})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
