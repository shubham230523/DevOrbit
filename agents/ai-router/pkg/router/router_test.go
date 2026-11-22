package router

import (
	"context"
	"errors"
	"testing"
)

func TestAIRouter_ModelRegistrationAndRouting(t *testing.T) {
	r := NewAIRouter()

	models := r.ListAvailableModels()
	if len(models) < 4 {
		t.Errorf("Expected at least 4 models registered, got %d", len(models))
	}

	provider, err := r.RoutePrompt("antigravity-gemini-3.8-flash", "Hello")
	if err != nil {
		t.Fatalf("Failed to route prompt: %v", err)
	}

	if provider != "Google Antigravity" {
		t.Errorf("Expected provider 'Google Antigravity', got '%s'", provider)
	}
}

func TestAIRouter_NoSilentMockFallbackWhenAPIKeyMissing(t *testing.T) {
	// Without mock flag and without API key -> MUST return ErrProviderUnavailable (HTTP 503)
	r := NewAIRouterWithOptions(AIRouterOptions{
		UseMockProvider: false,
		GeminiAPIKey:    "",
		Environment:     "production",
	})

	ctx := context.Background()
	_, err := r.Generate(ctx, GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Expected ErrProviderUnavailable (503 Service Unavailable) when GEMINI_API_KEY is missing, got: %v", err)
	}
}

func TestAIRouter_ExplicitMockProvider(t *testing.T) {
	// Explicit DEVORBIT_GEMINI_USE_MOCK=true -> Uses MockProvider
	r := NewAIRouterWithOptions(AIRouterOptions{
		UseMockProvider: true,
		Environment:     "development",
	})

	ctx := context.Background()
	resp, err := r.Generate(ctx, GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Expected successful generation with explicit MockProvider, got error: %v", err)
	}
	if resp.Text == "" {
		t.Errorf("Expected mock text response")
	}
}

func TestAIRouter_InvalidModel(t *testing.T) {
	r := NewAIRouter()

	_, err := r.RoutePrompt("unsupported-model-99", "Hello")
	if err == nil {
		t.Errorf("Expected error for non-existent model")
	}
}

func TestAIRouter_ProviderRegistrationAndGeneration(t *testing.T) {
	r := NewAIRouter()
	mockProvider := NewMockGeminiProvider()
	r.RegisterProvider("Gemini API", mockProvider)

	mockProvider.SetResponseForPrompt("inspect architecture", &GenerateResponse{
		Text:  "Architecture inspect response verified",
		Model: "gemini-2.5-flash",
	})

	ctx := context.Background()
	resp, err := r.Generate(ctx, GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Please inspect architecture"},
		},
	})
	if err != nil {
		t.Fatalf("Unexpected error generating content: %v", err)
	}

	if resp.Text != "Architecture inspect response verified" {
		t.Errorf("Unexpected response text: %s", resp.Text)
	}
}

func TestTools_StageCToolDeclarationsSchema(t *testing.T) {
	decls := GetStageCToolDeclarations()
	if len(decls) != 10 {
		t.Fatalf("Expected exactly 10 Stage C tool declarations, got %d", len(decls))
	}

	expectedTools := map[string]bool{
		ToolListProjects:     true,
		ToolListFiles:        true,
		ToolReadFile:         true,
		ToolSearchFiles:      true,
		ToolCreateFile:       true,
		ToolEditFile:         true,
		ToolDeleteFile:       true,
		ToolExecuteCommand:   true,
		ToolGetCommandOutput: true,
		ToolCancelTask:       true,
	}

	for _, d := range decls {
		if !expectedTools[d.Name] {
			t.Errorf("Unexpected tool name in declarations: %s", d.Name)
		}
		if d.Description == "" {
			t.Errorf("Tool %s missing description", d.Name)
		}
	}
}

func TestTools_ValidationAndParsing(t *testing.T) {
	validCall := &FunctionCall{
		Name: ToolReadFile,
		Arguments: map[string]any{
			"path": "src/main.go",
		},
	}
	if err := ValidateFunctionCall(validCall); err != nil {
		t.Fatalf("Expected valid call to pass, got: %v", err)
	}

	missingArgCall := &FunctionCall{
		Name: ToolReadFile,
		Arguments: map[string]any{
			"line_count": 10,
		},
	}
	if err := ValidateFunctionCall(missingArgCall); !errors.Is(err, ErrMissingRequiredArg) {
		t.Errorf("Expected ErrMissingRequiredArg, got: %v", err)
	}

	unknownCall := &FunctionCall{
		Name: "unregistered_dangerous_tool",
		Arguments: map[string]any{
			"path": "test",
		},
	}
	if err := ValidateFunctionCall(unknownCall); !errors.Is(err, ErrToolNotFound) {
		t.Errorf("Expected ErrToolNotFound, got: %v", err)
	}

	rawJSON := []byte(`[{"name":"create_file","arguments":{"path":"README.md","content":"# DevOrbit"}}]`)
	calls, err := ParseFunctionCallsFromJSON(rawJSON)
	if err != nil {
		t.Fatalf("Failed to parse function calls from JSON: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != ToolCreateFile {
		t.Errorf("Parsed calls unexpected: %+v", calls)
	}

	_, err = ParseFunctionCallsFromJSON([]byte(`not a json`))
	if err == nil {
		t.Errorf("Expected error for malformed JSON")
	}
}

func TestSecretRedaction(t *testing.T) {
	secret := "AIzaSySecretApiKey123456789"
	rawError := "Google GenAI error with key AIzaSySecretApiKey123456789: quota exceeded"

	redacted := RedactSecret(rawError, secret)
	if redacted != "Google GenAI error with key [REDACTED]: quota exceeded" {
		t.Errorf("Expected secret to be redacted, got: %s", redacted)
	}
}

func TestGeminiProvider_MissingAPIKey(t *testing.T) {
	t.Setenv("DEVORBIT_GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	_, err := NewGeminiProvider(context.Background(), GeminiConfig{})
	if !errors.Is(err, ErrMissingAPIKey) {
		t.Errorf("Expected ErrMissingAPIKey when no credentials supplied, got: %v", err)
	}
}

func TestMockGeminiProvider_FunctionCallingAndContinuationWorkflow(t *testing.T) {
	ctx := context.Background()
	mock := NewMockGeminiProvider()

	mock.SetResponseForPrompt("read config", &GenerateResponse{
		Model: "gemini-2.5-flash",
		FunctionCalls: []FunctionCall{
			{
				Name: ToolReadFile,
				Arguments: map[string]any{
					"path": "config.yaml",
				},
			},
		},
	})

	tools := GetStageCToolDeclarations()
	step1Resp, err := mock.Generate(ctx, GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Please read config for the app."},
		},
		Tools: tools,
	})
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	if len(step1Resp.FunctionCalls) != 1 {
		t.Fatalf("Expected 1 function call, got %d", len(step1Resp.FunctionCalls))
	}
	fc := step1Resp.FunctionCalls[0]
	if fc.Name != ToolReadFile {
		t.Errorf("Expected ToolReadFile, got %s", fc.Name)
	}

	if err := ValidateFunctionCall(&fc); err != nil {
		t.Fatalf("Function call schema validation failed: %v", err)
	}

	simulatedToolResult := map[string]any{
		"path":    "config.yaml",
		"content": "port: 8080\nenv: production",
	}

	step2Resp, err := mock.Generate(ctx, GenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Please read config for the app."},
			{Role: "model", FunctionCalls: []FunctionCall{fc}},
			{
				Role: "tool",
				FunctionResponses: []FunctionResponse{
					{
						Name:     ToolReadFile,
						Response: simulatedToolResult,
					},
				},
			},
		},
		Tools: tools,
	})
	if err != nil {
		t.Fatalf("Step 2 (continuation) failed: %v", err)
	}

	if step2Resp.Text == "" {
		t.Errorf("Expected final assistant text response after function result submitted")
	}
}
