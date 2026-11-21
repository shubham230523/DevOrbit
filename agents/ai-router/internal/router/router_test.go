package router

import (
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

func TestAIRouter_InvalidModel(t *testing.T) {
	r := NewAIRouter()

	_, err := r.RoutePrompt("unsupported-model-99", "Hello")
	if err == nil {
		t.Errorf("Expected error for non-existent model")
	}
}
