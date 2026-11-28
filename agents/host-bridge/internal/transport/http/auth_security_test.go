package http_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
)

type secTestEnv struct {
	t          *testing.T
	workspace  string
	hostSecret string
	server     *httptransport.Server
	mux        *http.ServeMux
	tlsServer  *httptest.Server
	httpServer *httptest.Server
	agentEng   *application.GeminiAgentEngine
	procSvc    *application.ProcessService
}

type secSessionCreds struct {
	sessionID string
	deviceID  string
	token     string
	secret    string
	authKey   []byte
	httpE2E   *security.E2ESession
}

func newSecTestEnv(t *testing.T) *secTestEnv {
	t.Helper()
	wsDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(wsDir, "README.md"), []byte("hello devorbit\n"), 0644)
	gitInit := exec.Command("git", "init")
	gitInit.Dir = wsDir
	_ = gitInit.Run()

	jail := security.NewWorkspaceJail([]string{wsDir})
	policy := security.NewDefaultCommandPolicy()
	hostSecret := "host-admin-secret-32-bytes-0123456"
	pairingEngine := security.NewPairingEngine("host-sec-01", "Sec Host", hostSecret)

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storage)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	srv := httptransport.NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	srv.SetPairingEngine(pairingEngine)
	srv.SetHostToken(hostSecret)
	srv.SetIdempotencyManager(security.NewIdempotencyManager(10 * time.Minute))

	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", router.NewMockGeminiProvider())
	srv.SetAIRouter(aiRouter)

	approvalMgr := application.NewApprovalManager()
	agentExec := application.NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)
	agentEng := application.NewGeminiAgentEngine(aiRouter, agentExec, approvalMgr)
	srv.SetAgentEngine(agentEng)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	tlsSrv := httptest.NewTLSServer(mux)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(func() {
		tlsSrv.Close()
		httpSrv.Close()
	})

	return &secTestEnv{
		t:          t,
		workspace:  wsDir,
		hostSecret: hostSecret,
		server:     srv,
		mux:        mux,
		tlsServer:  tlsSrv,
		httpServer: httpSrv,
		agentEng:   agentEng,
		procSvc:    psSvc,
	}
}

func (e *secTestEnv) registerSession(sessID, devID, token, secret string, ttl time.Duration, perms *security.SessionPermissions) *secSessionCreds {
	e.t.Helper()
	if _, err := e.server.SessionStore().RegisterSession(sessID, devID, token, secret, time.Now().UTC().Add(ttl), perms); err != nil {
		e.t.Fatalf("RegisterSession(%s) failed: %v", sessID, err)
	}
	authKey, err := security.DeriveHTTPRequestAuthKey(secret, sessID, devID)
	if err != nil {
		e.t.Fatalf("DeriveHTTPRequestAuthKey failed: %v", err)
	}
	httpE2E, err := security.NewChannelE2ESession(security.ChannelDirectHTTP, sessID, devID, secret, ttl)
	if err != nil {
		e.t.Fatalf("NewChannelE2ESession failed: %v", err)
	}
	return &secSessionCreds{
		sessionID: sessID,
		deviceID:  devID,
		token:     token,
		secret:    secret,
		authKey:   authKey,
		httpE2E:   httpE2E,
	}
}

func signOuterE2EHeaders(req *http.Request, body []byte, creds *secSessionCreds, ts string, nonce string) {
	sum := sha256.Sum256(body)
	sig := security.ComputeHTTPRequestMAC(
		creds.authKey,
		req.Method,
		req.URL.RequestURI(),
		creds.sessionID,
		creds.deviceID,
		ts,
		nonce,
		hex.EncodeToString(sum[:]),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httptransport.HeaderSessionID, creds.sessionID)
	req.Header.Set(httptransport.HeaderDeviceID, creds.deviceID)
	req.Header.Set(httptransport.HeaderTimestamp, ts)
	req.Header.Set(httptransport.HeaderNonce, nonce)
	req.Header.Set(httptransport.HeaderSignature, sig)
}

func signTLSHeaders(req *http.Request, body []byte, creds *secSessionCreds, ts string, nonce string) {
	sum := sha256.Sum256(body)
	sig := security.ComputeHTTPRequestMAC(
		creds.authKey,
		req.Method,
		req.URL.RequestURI(),
		creds.sessionID,
		creds.deviceID,
		ts,
		nonce,
		hex.EncodeToString(sum[:]),
	)
	req.Header.Set("Authorization", "Bearer "+creds.token)
	req.Header.Set(httptransport.HeaderSessionID, creds.sessionID)
	req.Header.Set(httptransport.HeaderDeviceID, creds.deviceID)
	req.Header.Set(httptransport.HeaderTimestamp, ts)
	req.Header.Set(httptransport.HeaderNonce, nonce)
	req.Header.Set(httptransport.HeaderSignature, sig)
}

func doTLSRequest(t *testing.T, client *http.Client, baseURL, method, path string, body []byte, configure func(*http.Request)) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, baseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("NewRequest(%s %s): %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if configure != nil {
		configure(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do(%s %s): %v", method, path, err)
	}
	return resp
}

func doSecE2ERequest(t *testing.T, baseURL string, creds *secSessionCreds, seq int64, method, path string, body []byte) (int, int, string) {
	t.Helper()
	ts := time.Now().UTC().UnixMicro()
	appReq := map[string]any{
		"v":          "1.2",
		"session_id": creds.sessionID,
		"request_id": fmt.Sprintf("sec_e2e_%d", seq),
		"method":     method,
		"path":       path,
		"body":       string(body),
		"seq_num":    seq,
		"timestamp":  ts,
	}
	appBytes, _ := json.Marshal(appReq)
	cipherHex, err := creds.httpE2E.Encrypt(security.DirClientToHost, appBytes, seq, ts)
	if err != nil {
		t.Fatalf("E2E Encrypt failed: %v", err)
	}
	frameBytes, _ := json.Marshal(map[string]any{
		"v":                 "1.2",
		"session_id":        creds.sessionID,
		"type":              "data",
		"encrypted_payload": cipherHex,
		"seq_num":           seq,
		"timestamp":         ts,
	})

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/e2e", bytes.NewReader(frameBytes))
	outerTS := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	outerNonce := fmt.Sprintf("e2e-%s-%d-%d", creds.sessionID, seq, time.Now().UTC().UnixNano())
	signOuterE2EHeaders(req, frameBytes, creds, outerTS, outerNonce)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("E2E HTTP Do failed: %v", err)
	}
	defer resp.Body.Close()

	rawResp, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, resp.StatusCode, string(rawResp)
	}

	var frame struct {
		EncryptedPayload string `json:"encrypted_payload"`
		SeqNum           int64  `json:"seq_num"`
		Timestamp        int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(rawResp, &frame); err != nil {
		t.Fatalf("unmarshal outer E2E frame failed: %v", err)
	}
	plainResp, err := creds.httpE2E.Decrypt(security.DirHostToClient, frame.EncryptedPayload, frame.SeqNum, frame.Timestamp)
	if err != nil {
		t.Fatalf("decrypt outer E2E response failed: %v", err)
	}
	var inner struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(plainResp, &inner); err != nil {
		t.Fatalf("unmarshal inner app response failed: %v", err)
	}
	return resp.StatusCode, inner.StatusCode, inner.Body
}

func TestAuthBoundary_EveryProtectedRoute(t *testing.T) {
	env := newSecTestEnv(t)
	tlsClient := env.tlsServer.Client()
	plainClient := env.httpServer.Client()

	fullCreds := env.registerSession("sess-full", "dev-full", "tok-full-32bytes-secret-value-001", "sec-full-32bytes-master-value-001!", time.Hour, security.NewFullSessionPermissions())
	scopedCreds := env.registerSession("sess-scoped", "dev-scoped", "tok-scoped-32bytes-secret-001!!", "sec-scoped-32bytes-master-001!!", time.Hour, security.NewScopedSessionPermissions())
	expCreds := env.registerSession("sess-exp", "dev-exp", "tok-exp-32bytes-secret-value-001!", "sec-exp-32bytes-master-value-001!!", 100*time.Millisecond, security.NewFullSessionPermissions())
	revCreds := env.registerSession("sess-rev", "dev-rev", "tok-rev-32bytes-secret-value-001!", "sec-rev-32bytes-master-value-001!!", time.Hour, security.NewFullSessionPermissions())
	_, _ = env.server.SessionStore().RevokeSession(revCreds.sessionID)
	time.Sleep(150 * time.Millisecond)

	// Seed a real process ID & agent task owned by fullCreds so process/task endpoints resolve cleanly when authorized
	procInfo, err := env.procSvc.ExecuteProcess(t.Context(), &domain.ProcessSpec{
		WorkspaceID:    env.workspace,
		Command:        "git",
		Args:           []string{"version"},
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("failed seeding process: %v", err)
	}
	_, _ = env.server.SessionStore().GetActiveSession(fullCreds.sessionID, fullCreds.deviceID)
	task, err := env.agentEng.CreateTaskForOwner(env.workspace, "summarize repo", "gemini-2.5-flash", fullCreds.sessionID, fullCreds.deviceID)
	if err != nil {
		t.Fatalf("failed seeding agent task: %v", err)
	}

	routes := []struct {
		name           string
		method         string
		path           string
		body           []byte
		wrongMethod    string
		unauthStatus   int
		expectedAccept []int
	}{
		{"workspaces_list", http.MethodGet, "/api/v1/workspaces", nil, http.MethodPost, http.StatusUnauthorized, []int{http.StatusOK}},
		{"workspaces_discover", http.MethodPost, "/api/v1/workspaces/discover", []byte(`{}`), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"files_list", http.MethodPost, "/api/v1/files/list", []byte(fmt.Sprintf(`{"workspace_id":%q,"relative_path":"."}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"files_read", http.MethodPost, "/api/v1/files/read", []byte(fmt.Sprintf(`{"workspace_id":%q,"relative_path":"README.md"}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"files_write", http.MethodPost, "/api/v1/files/write", []byte(fmt.Sprintf(`{"workspace_id":%q,"relative_path":"README.md","new_content":"updated"}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK, http.StatusConflict}},
		{"git_status", http.MethodPost, "/api/v1/git/status", []byte(fmt.Sprintf(`{"workspace_id":%q}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"git_diff", http.MethodPost, "/api/v1/git/diff", []byte(fmt.Sprintf(`{"workspace_id":%q}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"processes_execute", http.MethodPost, "/api/v1/processes/execute", []byte(fmt.Sprintf(`{"workspace_id":%q,"command":"git","args":["version"]}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"processes_status", http.MethodGet, "/api/v1/processes/" + procInfo.ID, nil, http.MethodPost, http.StatusUnauthorized, []int{http.StatusNotFound}}, // not created via HTTP handler, so owner check returns 404 after passing authN/authZ
		{"processes_cancel", http.MethodPost, "/api/v1/processes/" + procInfo.ID + "/cancel", nil, http.MethodGet, http.StatusUnauthorized, []int{http.StatusNotFound}},
		{"devices_adb", http.MethodGet, "/api/v1/devices/adb", nil, http.MethodPost, http.StatusUnauthorized, []int{http.StatusOK}},
		{"ai_prompt", http.MethodPost, "/api/v1/ai/prompt", []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"agent_task_create", http.MethodPost, "/api/v1/agent/tasks", []byte(fmt.Sprintf(`{"workspace_id":%q,"prompt":"check"}`, env.workspace)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"agent_task_get", http.MethodGet, "/api/v1/agent/tasks/" + task.ID, nil, http.MethodPost, http.StatusUnauthorized, []int{http.StatusOK}},
		{"agent_task_approve", http.MethodPost, "/api/v1/agent/tasks/approve", []byte(fmt.Sprintf(`{"task_id":%q,"approval_token":"bad"}`, task.ID)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusConflict}},
		{"agent_task_cancel", http.MethodPost, "/api/v1/agent/tasks/cancel", []byte(fmt.Sprintf(`{"task_id":%q}`, task.ID)), http.MethodGet, http.StatusUnauthorized, []int{http.StatusOK}},
		{"github_repos", http.MethodGet, "/api/v1/github/repos", nil, http.MethodPost, http.StatusUnauthorized, []int{http.StatusUnauthorized}}, // missing X-GitHub-Token still passes Host Bridge session auth and fails at GitHub token check
		{"github_clone", http.MethodPost, "/api/v1/github/clone", []byte(`{"name":"../bad","clone_url":"https://github.com/org/repo.git"}`), http.MethodGet, http.StatusUnauthorized, []int{http.StatusBadRequest}},
	}

	nonceCounter := 0
	nextNonce := func(prefix string) string {
		nonceCounter++
		return fmt.Sprintf("%s-nonce-%06d", prefix, nonceCounter)
	}

	e2eSeq := int64(1)

	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			// 1. No credentials (plaintext HTTP and TLS) -> 401
			resp := doTLSRequest(t, plainClient, env.httpServer.URL, tc.method, tc.path, tc.body, nil)
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("plaintext no-creds expected 401, got %d", resp.StatusCode)
			}
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, nil)
			resp.Body.Close()
			if resp.StatusCode != tc.unauthStatus {
				t.Fatalf("TLS no-creds expected %d, got %d", tc.unauthStatus, resp.StatusCode)
			}

			// 2. Query-string credentials rejected -> 400
			sep := "?"
			if strings.Contains(tc.path, "?") {
				sep = "&"
			}
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path+sep+"token="+fullCreds.token, tc.body, nil)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("query token expected 400, got %d", resp.StatusCode)
			}

			// 3. Malformed credentials -> 401
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("malformed auth expected 401, got %d", resp.StatusCode)
			}

			// 4. Invalid / unknown token -> 401
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer unknown-session-token")
				r.Header.Set(httptransport.HeaderSessionID, fullCreds.sessionID)
				r.Header.Set(httptransport.HeaderDeviceID, fullCreds.deviceID)
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("unknown token expected 401, got %d", resp.StatusCode)
			}

			// 5. Expired session -> 401
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, expCreds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), nextNonce("exp"))
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expired session expected 401, got %d", resp.StatusCode)
			}

			// 6. Revoked session -> 403
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, revCreds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), nextNonce("rev"))
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("revoked session expected 403, got %d", resp.StatusCode)
			}

			// 7. Wrong device / session binding -> 403
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+fullCreds.token)
				r.Header.Set(httptransport.HeaderSessionID, fullCreds.sessionID)
				r.Header.Set(httptransport.HeaderDeviceID, "wrong-device-id")
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("wrong device binding expected 403, got %d", resp.StatusCode)
			}

			// 8. Plaintext HTTP rejected even with valid Bearer + HMAC signature -> 403
			resp = doTLSRequest(t, plainClient, env.httpServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, fullCreds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), nextNonce("plain"))
				r.Header.Set("X-Forwarded-Proto", "https") // untrusted forwarded spoof must be ignored
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("plaintext direct HTTP with valid creds expected 403, got %d", resp.StatusCode)
			}

			// 9. Tampered HMAC signature -> 401
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, fullCreds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), nextNonce("badsig"))
				r.Header.Set(httptransport.HeaderSignature, "deadbeef")
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("tampered signature expected 401, got %d", resp.StatusCode)
			}

			// 10. Stale timestamp -> 401
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				staleTS := strconv.FormatInt(time.Now().UTC().Add(-15*time.Minute).UnixMicro(), 10)
				signTLSHeaders(r, tc.body, fullCreds, staleTS, nextNonce("stale"))
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("stale timestamp expected 401, got %d", resp.StatusCode)
			}

			// 11. Caller without required permission -> 403
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, scopedCreds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), nextNonce("noperms"))
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("missing permission expected 403, got %d", resp.StatusCode)
			}

			// 12. Valid TLS+HMAC request accepted (and replaying the same nonce rejected with 403)
			replayNonce := nextNonce("valid")
			replayTS := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
			resp = doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, fullCreds, replayTS, replayNonce)
			})
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			accepted := false
			for _, want := range tc.expectedAccept {
				if resp.StatusCode == want {
					accepted = true
					break
				}
			}
			if tc.name == "github_repos" {
				// GitHub endpoint passes Host Bridge auth and only returns 401 from github_handlers.go token check
				if !strings.Contains(string(respBody), "GitHub token required") {
					t.Fatalf("github_repos expected Host Bridge auth pass + GitHub PAT prompt, got %d: %s", resp.StatusCode, string(respBody))
				}
				accepted = true
			}
			if !accepted {
				t.Fatalf("authorized TLS request failed unexpectedly: status=%d body=%s", resp.StatusCode, string(respBody))
			}

			// Replayed nonce must fail with 403
			respReplay := doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.method, tc.path, tc.body, func(r *http.Request) {
				signTLSHeaders(r, tc.body, fullCreds, replayTS, replayNonce)
			})
			respReplay.Body.Close()
			if respReplay.StatusCode != http.StatusForbidden {
				t.Fatalf("replayed nonce expected 403, got %d", respReplay.StatusCode)
			}

			// 13. Wrong HTTP method even when authenticated -> 405
			if tc.wrongMethod != "" {
				respMeth := doTLSRequest(t, tlsClient, env.tlsServer.URL, tc.wrongMethod, tc.path, nil, func(r *http.Request) {
					signTLSHeaders(r, nil, fullCreds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), nextNonce("meth"))
				})
				respMeth.Body.Close()
				if respMeth.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("wrong method %s expected 405, got %d", tc.wrongMethod, respMeth.StatusCode)
				}
			}

			// 14. Valid E2E LAN tunnel request over plaintext HTTP accepted
			outerCode, innerCode, innerBody := doSecE2ERequest(t, env.httpServer.URL, fullCreds, e2eSeq, tc.method, tc.path, tc.body)
			e2eSeq++
			if outerCode != http.StatusOK {
				t.Fatalf("E2E outer status expected 200, got %d (%s)", outerCode, innerBody)
			}
			if tc.name != "github_repos" && (innerCode == http.StatusUnauthorized || innerCode == http.StatusForbidden) {
				t.Fatalf("E2E authorized request unexpectedly got auth error %d: %s", innerCode, innerBody)
			}
		})
	}
}

func TestAuthBoundary_HostAdminAndE2ETunnelChecks(t *testing.T) {
	env := newSecTestEnv(t)
	creds := env.registerSession("sess-adm-01", "dev-adm-01", "tok-adm-32bytes-secret-value-001!", "sec-adm-32bytes-master-value-001!", time.Hour, security.NewFullSessionPermissions())

	// Host-admin routes reject missing auth and reject mobile session tokens
	for _, path := range []string{"/api/v1/pairing/challenge", "/api/v1/sessions/revoke"} {
		res1, err := http.Post(env.httpServer.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("POST %s failed: %v", path, err)
		}
		res1.Body.Close()
		if res1.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated expected 401, got %d", path, res1.StatusCode)
		}

		reqMob, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+path, strings.NewReader(`{}`))
		reqMob.Header.Set("Authorization", "Bearer "+creds.token)
		resMob, err := http.DefaultClient.Do(reqMob)
		if err != nil {
			t.Fatalf("mobile auth POST %s failed: %v", path, err)
		}
		resMob.Body.Close()
		if resMob.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s with mobile token expected 401, got %d", path, resMob.StatusCode)
		}
	}

	// Unauthenticated /api/v1/health exposes minimal payload only
	hResp, err := http.Get(env.httpServer.URL + "/api/v1/health")
	if err != nil || hResp.StatusCode != http.StatusOK {
		t.Fatalf("public health check failed: %v", err)
	}
	var pubHealth map[string]any
	_ = json.NewDecoder(hResp.Body).Decode(&pubHealth)
	hResp.Body.Close()
	if _, leaksOS := pubHealth["os"]; leaksOS {
		t.Fatalf("public health endpoint must not leak OS/hostname metadata: %+v", pubHealth)
	}

	// E2E authenticated /api/v1/health returns full metadata
	outer, inner, body := doSecE2ERequest(t, env.httpServer.URL, creds, 1, http.MethodGet, "/api/v1/health", nil)
	if outer != http.StatusOK || inner != http.StatusOK || !strings.Contains(body, "allowed_workspaces_count") {
		t.Fatalf("expected authenticated E2E health metadata, got outer=%d inner=%d body=%s", outer, inner, body)
	}

	// Spoofed Authorization header inside E2E tunnel cannot elevate to host-admin pairing challenge
	ts := time.Now().UTC().UnixMicro()
	appReq := map[string]any{
		"v":          "1.2",
		"session_id": creds.sessionID,
		"request_id": "esc_req",
		"method":     http.MethodPost,
		"path":       "/api/v1/pairing/challenge",
		"headers":    map[string]string{"Authorization": "Bearer " + env.hostSecret},
		"seq_num":    int64(2),
		"timestamp":  ts,
	}
	appBytes, _ := json.Marshal(appReq)
	cipherHex, _ := creds.httpE2E.Encrypt(security.DirClientToHost, appBytes, 2, ts)
	frameBytes, _ := json.Marshal(map[string]any{
		"v":                 "1.2",
		"session_id":        creds.sessionID,
		"type":              "data",
		"encrypted_payload": cipherHex,
		"seq_num":           int64(2),
		"timestamp":         ts,
	})
	reqEsc, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+"/api/v1/e2e", bytes.NewReader(frameBytes))
	escTS := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	signOuterE2EHeaders(reqEsc, frameBytes, creds, escTS, "nonce-esc-0001")
	escResp, err := http.DefaultClient.Do(reqEsc)
	if err != nil {
		t.Fatalf("E2E escalation request failed: %v", err)
	}
	defer escResp.Body.Close()
	var escFrame struct {
		EncryptedPayload string `json:"encrypted_payload"`
		SeqNum           int64  `json:"seq_num"`
		Timestamp        int64  `json:"timestamp"`
	}
	_ = json.NewDecoder(escResp.Body).Decode(&escFrame)
	plainEsc, _ := creds.httpE2E.Decrypt(security.DirHostToClient, escFrame.EncryptedPayload, escFrame.SeqNum, escFrame.Timestamp)
	if !strings.Contains(string(plainEsc), "401") {
		t.Fatalf("expected tunneled Authorization header to be stripped and return 401, got: %s", string(plainEsc))
	}

	// Replayed outer E2E request must be rejected with 403
	reqReplay, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+"/api/v1/e2e", bytes.NewReader(frameBytes))
	signOuterE2EHeaders(reqReplay, frameBytes, creds, escTS, "nonce-esc-0001")
	repResp, err := http.DefaultClient.Do(reqReplay)
	if err != nil {
		t.Fatalf("E2E replay request failed: %v", err)
	}
	repResp.Body.Close()
	if repResp.StatusCode != http.StatusForbidden {
		t.Fatalf("replayed outer E2E request expected 403, got %d", repResp.StatusCode)
	}

	// Replayed inner E2E seq_num 2 with a fresh outer nonce must ALSO be rejected with 403
	reqInnerReplay, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+"/api/v1/e2e", bytes.NewReader(frameBytes))
	signOuterE2EHeaders(reqInnerReplay, frameBytes, creds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), "nonce-esc-replay-inner")
	repInnerResp, err := http.DefaultClient.Do(reqInnerReplay)
	if err != nil {
		t.Fatalf("E2E inner replay request failed: %v", err)
	}
	repInnerResp.Body.Close()
	if repInnerResp.StatusCode != http.StatusForbidden {
		t.Fatalf("replayed inner E2E frame seq_num expected 403, got %d", repInnerResp.StatusCode)
	}

	// Sending raw Authorization: Bearer on /api/v1/e2e must be rejected with 401
	reqRawBearer, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+"/api/v1/e2e", bytes.NewReader(frameBytes))
	signOuterE2EHeaders(reqRawBearer, frameBytes, creds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), "nonce-raw-bearer-0001")
	reqRawBearer.Header.Set("Authorization", "Bearer "+creds.token)
	rawBearerResp, err := http.DefaultClient.Do(reqRawBearer)
	if err != nil {
		t.Fatalf("E2E raw bearer test request failed: %v", err)
	}
	rawBearerResp.Body.Close()
	if rawBearerResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("raw bearer header on /api/v1/e2e expected 401, got %d", rawBearerResp.StatusCode)
	}

	// Cross-channel BrokerWS key cannot decrypt on DirectHTTP /api/v1/e2e
	wsE2E, _ := security.NewChannelE2ESession(security.ChannelBrokerWS, creds.sessionID, creds.deviceID, creds.secret, time.Hour)
	tsWS := time.Now().UTC().UnixMicro()
	wsAppBytes, _ := json.Marshal(map[string]any{
		"v": "1.2", "session_id": creds.sessionID, "request_id": "cross", "method": "GET", "path": "/api/v1/workspaces", "seq_num": int64(3), "timestamp": tsWS,
	})
	wsCipher, _ := wsE2E.Encrypt(security.DirClientToHost, wsAppBytes, 3, tsWS)
	wsFrame, _ := json.Marshal(map[string]any{
		"v": "1.2", "session_id": creds.sessionID, "type": "data", "encrypted_payload": wsCipher, "seq_num": int64(3), "timestamp": tsWS,
	})
	reqCross, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+"/api/v1/e2e", bytes.NewReader(wsFrame))
	signOuterE2EHeaders(reqCross, wsFrame, creds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), "nonce-cross-0001")
	crossResp, err := http.DefaultClient.Do(reqCross)
	if err != nil {
		t.Fatalf("E2E cross-channel request failed: %v", err)
	}
	crossResp.Body.Close()
	if crossResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-channel BrokerWS frame expected 401 on /api/v1/e2e, got %d", crossResp.StatusCode)
	}

	// Nested /api/v1/e2e must be rejected with 400
	outerNested, _, bodyNested := doSecE2ERequest(t, env.httpServer.URL, creds, 4, http.MethodPost, "/api/v1/e2e", []byte(`{}`))
	if outerNested != http.StatusBadRequest {
		t.Fatalf("nested /api/v1/e2e expected 400, got %d (%s)", outerNested, bodyNested)
	}
}

func TestAuthBoundary_OwnershipIsolationSideEffectsAndRevocation(t *testing.T) {
	env := newSecTestEnv(t)
	credsA := env.registerSession("sess-owner-A", "dev-owner-A", "tok-ownerA-32bytes-secret-001!!!", "sec-ownerA-32bytes-master-001!!!", time.Hour, security.NewFullSessionPermissions())
	credsB := env.registerSession("sess-owner-B", "dev-owner-B", "tok-ownerB-32bytes-secret-001!!!", "sec-ownerB-32bytes-master-001!!!", time.Hour, security.NewFullSessionPermissions())

	// 1. Unauthorized attempt to modify README.md must NOT alter disk content
	unauthWriteBody := []byte(fmt.Sprintf(`{"workspace_id":%q,"relative_path":"README.md","new_content":"PWNED"}`, env.workspace))
	respUnauth := doTLSRequest(t, env.tlsServer.Client(), env.tlsServer.URL, http.MethodPost, "/api/v1/files/write", unauthWriteBody, nil)
	respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated write, got %d", respUnauth.StatusCode)
	}
	diskBytes, _ := os.ReadFile(filepath.Join(env.workspace, "README.md"))
	if string(diskBytes) != "hello devorbit\n" {
		t.Fatalf("unauthorized write modified file on disk: %q", string(diskBytes))
	}

	// 2. Session A executes a process via E2E -> Session B cannot inspect or cancel it
	_, codeExec, bodyExec := doSecE2ERequest(t, env.httpServer.URL, credsA, 1, http.MethodPost, "/api/v1/processes/execute",
		[]byte(fmt.Sprintf(`{"workspace_id":%q,"command":"git","args":["version"]}`, env.workspace)))
	if codeExec != http.StatusOK {
		t.Fatalf("Session A process execute failed: %d %s", codeExec, bodyExec)
	}
	var procResp struct {
		ID string `json:"process_id"`
	}
	_ = json.Unmarshal([]byte(bodyExec), &procResp)
	if procResp.ID == "" {
		t.Fatalf("expected process ID, got: %s", bodyExec)
	}

	_, codeGetB, _ := doSecE2ERequest(t, env.httpServer.URL, credsB, 1, http.MethodGet, "/api/v1/processes/"+procResp.ID, nil)
	if codeGetB != http.StatusForbidden {
		t.Fatalf("expected 403 when Session B inspects Session A process, got %d", codeGetB)
	}
	_, codeCancelB, _ := doSecE2ERequest(t, env.httpServer.URL, credsB, 2, http.MethodPost, "/api/v1/processes/"+procResp.ID+"/cancel", nil)
	if codeCancelB != http.StatusForbidden {
		t.Fatalf("expected 403 when Session B cancels Session A process, got %d", codeCancelB)
	}

	// 3. Session A creates an agent task (approval_required) -> Session B cannot read, approve, or cancel it
	task, err := env.agentEng.CreateTaskForOwner(env.workspace, "modify files", "gemini-2.5-flash", credsA.sessionID, credsA.deviceID)
	if err != nil {
		t.Fatalf("CreateTaskForOwner failed: %v", err)
	}
	task.Status = "approval_required"
	task.ApprovalToken = "secret-approval-tok-01"

	_, codeTaskGetB, _ := doSecE2ERequest(t, env.httpServer.URL, credsB, 3, http.MethodGet, "/api/v1/agent/tasks/"+task.ID, nil)
	if codeTaskGetB != http.StatusForbidden {
		t.Fatalf("expected 403 when Session B reads Session A task, got %d", codeTaskGetB)
	}
	_, codeTaskApproveB, _ := doSecE2ERequest(t, env.httpServer.URL, credsB, 4, http.MethodPost, "/api/v1/agent/tasks/approve",
		[]byte(fmt.Sprintf(`{"task_id":%q,"approval_token":%q}`, task.ID, task.ApprovalToken)))
	if codeTaskApproveB != http.StatusForbidden {
		t.Fatalf("expected 403 when Session B approves Session A task, got %d", codeTaskApproveB)
	}
	_, codeTaskCancelB, _ := doSecE2ERequest(t, env.httpServer.URL, credsB, 5, http.MethodPost, "/api/v1/agent/tasks/cancel",
		[]byte(fmt.Sprintf(`{"task_id":%q}`, task.ID)))
	if codeTaskCancelB != http.StatusForbidden {
		t.Fatalf("expected 403 when Session B cancels Session A task, got %d", codeTaskCancelB)
	}

	// 4. Broker-mediated dispatch via ServeBrokerRequest works for active Session A
	brokerRec := httptest.NewRecorder()
	brokerReq := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	if !env.server.ServeBrokerRequest(brokerRec, brokerReq, credsA.sessionID, credsA.deviceID, nil) || brokerRec.Code != http.StatusOK {
		t.Fatalf("expected ServeBrokerRequest for active Session A to return 200, got %d (%s)", brokerRec.Code, brokerRec.Body.String())
	}

	// 5. Revoke Session A via host-admin /api/v1/sessions/revoke -> closes TLS, E2E, AND Broker access while Session B stays active
	revReq, _ := http.NewRequest(http.MethodPost, env.httpServer.URL+"/api/v1/sessions/revoke",
		strings.NewReader(fmt.Sprintf(`{"session_id":%q}`, credsA.sessionID)))
	revReq.Header.Set("Authorization", "Bearer "+env.hostSecret)
	revReq.Header.Set("Content-Type", "application/json")
	revResp, err := http.DefaultClient.Do(revReq)
	if err != nil || revResp.StatusCode != http.StatusOK {
		t.Fatalf("host-admin revoke failed: %v", err)
	}
	revResp.Body.Close()

	// Session A blocked via E2E
	outerAfterRev, _, _ := doSecE2ERequest(t, env.httpServer.URL, credsA, 2, http.MethodGet, "/api/v1/workspaces", nil)
	if outerAfterRev != http.StatusForbidden {
		t.Fatalf("expected 403 on E2E for revoked Session A, got %d", outerAfterRev)
	}

	// Session A blocked via TLS direct
	tlsAfterRev := doTLSRequest(t, env.tlsServer.Client(), env.tlsServer.URL, http.MethodGet, "/api/v1/workspaces", nil, func(r *http.Request) {
		signTLSHeaders(r, nil, credsA, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), "revoked-tls-nonce-01")
	})
	tlsAfterRev.Body.Close()
	if tlsAfterRev.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on TLS for revoked Session A, got %d", tlsAfterRev.StatusCode)
	}

	// Session A blocked via ServeBrokerRequest
	brokerRevRec := httptest.NewRecorder()
	brokerRevReq := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	if env.server.ServeBrokerRequest(brokerRevRec, brokerRevReq, credsA.sessionID, credsA.deviceID, nil) || brokerRevRec.Code != http.StatusForbidden {
		t.Fatalf("expected ServeBrokerRequest for revoked Session A to fail with 403, got %d", brokerRevRec.Code)
	}

	// Session B unaffected
	outerB, innerB, _ := doSecE2ERequest(t, env.httpServer.URL, credsB, 6, http.MethodGet, "/api/v1/workspaces", nil)
	if outerB != http.StatusOK || innerB != http.StatusOK {
		t.Fatalf("expected unrelated Session B to remain functional, got outer=%d inner=%d", outerB, innerB)
	}
}
