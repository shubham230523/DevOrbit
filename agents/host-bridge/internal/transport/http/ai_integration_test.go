package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
)

func setupTestServerWithAIRouter(t *testing.T) (*httptransport.Server, *router.AIRouter, *router.MockGeminiProvider) {
	jail := security.NewWorkspaceJail([]string{t.TempDir()})
	policy := security.NewDefaultCommandPolicy()
	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storage)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	server := httptransport.NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)

	aiRouter := router.NewAIRouter()
	mockProvider := router.NewMockGeminiProvider()
	aiRouter.RegisterProvider("Gemini API", mockProvider)
	server.SetAIRouter(aiRouter)

	return server, aiRouter, mockProvider
}

func postEncryptedE2E(t *testing.T, serverURL string, e2e *security.E2ESession, token string, seq int64, method, path string, body []byte) (int, []byte) {
	t.Helper()
	ts := time.Now().UTC().UnixMicro()
	appReq := map[string]any{
		"v":          "1.2",
		"session_id": e2e.SessionID,
		"request_id": "ai_test_req",
		"method":     method,
		"path":       path,
		"body":       string(body),
		"seq_num":    seq,
		"timestamp":  ts,
	}
	appBytes, _ := json.Marshal(appReq)
	cipherHex, err := e2e.Encrypt(security.DirClientToHost, appBytes, seq, ts)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	frameBytes, _ := json.Marshal(map[string]any{
		"v":                 "1.2",
		"session_id":        e2e.SessionID,
		"type":              "data",
		"encrypted_payload": cipherHex,
		"seq_num":           seq,
		"timestamp":         ts,
	})
	req, _ := http.NewRequest(http.MethodPost, serverURL+"/api/v1/e2e", bytes.NewReader(frameBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(httptransport.HeaderSessionID, e2e.SessionID)
	req.Header.Set(httptransport.HeaderDeviceID, e2e.DeviceID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("E2E HTTP request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected outer E2E HTTP 200, got %d", resp.StatusCode)
	}

	var respFrame struct {
		EncryptedPayload string `json:"encrypted_payload"`
		SeqNum           int64  `json:"seq_num"`
		Timestamp        int64  `json:"timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respFrame); err != nil {
		t.Fatalf("Decode outer E2E frame failed: %v", err)
	}
	plainResp, err := e2e.Decrypt(security.DirHostToClient, respFrame.EncryptedPayload, respFrame.SeqNum, respFrame.Timestamp)
	if err != nil {
		t.Fatalf("Decrypt outer E2E response failed: %v", err)
	}
	var appResp struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(plainResp, &appResp); err != nil {
		t.Fatalf("Unmarshal inner app response failed: %v", err)
	}
	return appResp.StatusCode, []byte(appResp.Body)
}

// TestHostBridge_AIMockedPromptAndFunctionCallingWorkflow verifies the full 7-step mocked workflow
// executed over the authenticated E2E HTTP transport boundary.
func TestHostBridge_AIMockedPromptAndFunctionCallingWorkflow(t *testing.T) {
	server, _, mockProvider := setupTestServerWithAIRouter(t)

	sessID := "sess-ai-01"
	devID := "dev-ai-01"
	token := "token-ai-32bytes-secret-value-01!"
	secret := "secret-ai-32bytes-master-value-01!"
	if _, err := server.SessionStore().RegisterSession(sessID, devID, token, secret, time.Now().UTC().Add(time.Hour), security.NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}
	clientE2E, err := security.NewChannelE2ESession(security.ChannelDirectHTTP, sessID, devID, secret, time.Hour)
	if err != nil {
		t.Fatalf("NewChannelE2ESession failed: %v", err)
	}

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Step 3 configuration: Provider will return a function call for "read app source"
	mockProvider.SetResponseForPrompt("read app source", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		FunctionCalls: []router.FunctionCall{
			{
				Name: router.ToolReadFile,
				Arguments: map[string]any{
					"path": "src/main.dart",
				},
			},
		},
	})

	// 1. Host Bridge receives a user prompt via authenticated E2E HTTP
	promptReq1 := router.GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []router.Message{
			{Role: "user", Content: "Please read app source to inspect configuration."},
		},
	}
	bodyBytes1, _ := json.Marshal(promptReq1)

	// Unauthenticated direct POST must be rejected with 401
	unauthResp, err := http.Post(ts.URL+"/api/v1/ai/prompt", "application/json", bytes.NewReader(bodyBytes1))
	if err != nil {
		t.Fatalf("Failed unauth post: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 for unauthenticated AI prompt, got: %d", unauthResp.StatusCode)
	}

	status1, respBody1 := postEncryptedE2E(t, ts.URL, clientE2E, token, 1, http.MethodPost, "/api/v1/ai/prompt", bodyBytes1)
	if status1 != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got: %d (%s)", status1, string(respBody1))
	}

	// 4. Host Bridge receives the structured request without executing it
	var genResp1 router.GenerateResponse
	if err := json.Unmarshal(respBody1, &genResp1); err != nil {
		t.Fatalf("Failed to decode response 1: %v", err)
	}

	if len(genResp1.FunctionCalls) != 1 {
		t.Fatalf("Expected 1 function call request, got %d", len(genResp1.FunctionCalls))
	}
	fc := genResp1.FunctionCalls[0]
	if fc.Name != router.ToolReadFile {
		t.Errorf("Expected tool '%s', got '%s'", router.ToolReadFile, fc.Name)
	}
	if fc.Arguments["path"] != "src/main.dart" {
		t.Errorf("Expected path 'src/main.dart', got: %v", fc.Arguments["path"])
	}

	// Validate function call structure without executing project tools
	if err := router.ValidateFunctionCall(&fc); err != nil {
		t.Fatalf("Function call validation failed: %v", err)
	}

	// 5. A simulated function result is submitted
	simulatedToolResult := map[string]any{
		"path":    "src/main.dart",
		"content": "void main() => runApp(const MyApp());",
	}

	// Configure mock continuation response for step 6 & 7
	mockProvider.SetResponseForPrompt(router.ToolReadFile, &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "I have examined src/main.dart and verified the app entry point.",
	})

	// 6. Gemini provider receives the result via continuation turn
	promptReq2 := router.GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []router.Message{
			{Role: "user", Content: "Please read app source to inspect configuration."},
			{Role: "model", FunctionCalls: []router.FunctionCall{fc}},
			{
				Role: "tool",
				FunctionResponses: []router.FunctionResponse{
					{
						Name:     router.ToolReadFile,
						Response: simulatedToolResult,
					},
				},
			},
		},
	}
	bodyBytes2, _ := json.Marshal(promptReq2)

	status2, respBody2 := postEncryptedE2E(t, ts.URL, clientE2E, token, 2, http.MethodPost, "/api/v1/ai/prompt", bodyBytes2)
	if status2 != http.StatusOK {
		t.Fatalf("Expected HTTP 200 on continuation, got: %d (%s)", status2, string(respBody2))
	}

	// 7. A final assistant response is returned
	var genResp2 router.GenerateResponse
	if err := json.Unmarshal(respBody2, &genResp2); err != nil {
		t.Fatalf("Failed to decode response 2: %v", err)
	}

	expectedFinalText := "I have examined src/main.dart and verified the app entry point."
	if genResp2.Text != expectedFinalText {
		t.Errorf("Expected final assistant response '%s', got '%s'", expectedFinalText, genResp2.Text)
	}
}

func TestHostBridge_AIEndpointErrorHandling(t *testing.T) {
	// Server without AI Router configured
	jail := security.NewWorkspaceJail([]string{t.TempDir()})
	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(security.NewDefaultCommandPolicy(), jail)
	server := httptransport.NewServer(
		application.NewWorkspaceService(jail),
		application.NewFilesystemService(storage),
		application.NewProcessService(procRunner),
		application.NewGitService(git.NewGitClient(jail)),
		application.NewDeviceService(adb.NewADBClient("adb")),
	)

	sessID := "sess-ai-err-01"
	devID := "dev-ai-err-01"
	token := "token-ai-err-32bytes-secret-val!"
	secret := "secret-ai-err-32bytes-master-val!"
	if _, err := server.SessionStore().RegisterSession(sessID, devID, token, secret, time.Now().UTC().Add(time.Hour), security.NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}
	clientE2E, err := security.NewChannelE2ESession(security.ChannelDirectHTTP, sessID, devID, secret, time.Hour)
	if err != nil {
		t.Fatalf("NewChannelE2ESession failed: %v", err)
	}

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	status, _ := postEncryptedE2E(t, ts.URL, clientE2E, token, 1, http.MethodPost, "/api/v1/ai/prompt", []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusServiceUnavailable {
		t.Errorf("Expected HTTP 503 when AI router not configured, got: %d", status)
	}
}
