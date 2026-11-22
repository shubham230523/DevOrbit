package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

// TestHostBridge_AIMockedPromptAndFunctionCallingWorkflow verifies the full 7-step mocked workflow:
// 1. Host Bridge receives a user prompt.
// 2. AI Router sends it to the Gemini provider.
// 3. Provider returns a function-call request.
// 4. Host Bridge receives the structured request without executing it.
// 5. A simulated function result is submitted.
// 6. Gemini provider receives the result.
// 7. A final assistant response is returned.
func TestHostBridge_AIMockedPromptAndFunctionCallingWorkflow(t *testing.T) {
	server, _, mockProvider := setupTestServerWithAIRouter(t)

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

	// 1. Host Bridge receives a user prompt via HTTP
	promptReq1 := router.GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []router.Message{
			{Role: "user", Content: "Please read app source to inspect configuration."},
		},
	}
	bodyBytes1, _ := json.Marshal(promptReq1)

	resp1, err := http.Post(ts.URL+"/api/v1/ai/prompt", "application/json", bytes.NewReader(bodyBytes1))
	if err != nil {
		t.Fatalf("Failed to post prompt: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got: %d", resp1.StatusCode)
	}

	// 4. Host Bridge receives the structured request without executing it
	var genResp1 router.GenerateResponse
	if err := json.NewDecoder(resp1.Body).Decode(&genResp1); err != nil {
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

	resp2, err := http.Post(ts.URL+"/api/v1/ai/prompt", "application/json", bytes.NewReader(bodyBytes2))
	if err != nil {
		t.Fatalf("Failed to post continuation: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 on continuation, got: %d", resp2.StatusCode)
	}

	// 7. A final assistant response is returned
	var genResp2 router.GenerateResponse
	if err := json.NewDecoder(resp2.Body).Decode(&genResp2); err != nil {
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

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/ai/prompt", "application/json", bytes.NewReader([]byte(`{"messages":[{"role":"user","content":"hi"}]}`)))
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected HTTP 503 when AI router not configured, got: %d", resp.StatusCode)
	}
}
