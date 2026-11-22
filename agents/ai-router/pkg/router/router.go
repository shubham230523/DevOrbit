package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type AIModel struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	IntegrationType string `json:"integration_type"` // "acp_agent" or "direct_api"
	IsAvailable     bool   `json:"is_available"`
}

type AIRouter struct {
	mu        sync.RWMutex
	models    map[string]AIModel
	providers map[string]Provider
}

func NewAIRouter() *AIRouter {
	r := &AIRouter{
		models:    make(map[string]AIModel),
		providers: make(map[string]Provider),
	}

	// Register models according to requirements
	r.RegisterModel(AIModel{ID: "gemini-2.5-flash", Provider: "Gemini API", Name: "Gemini 2.5 Flash", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "gemini-2.5-pro", Provider: "Gemini API", Name: "Gemini 2.5 Pro", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "antigravity-gemini-3.8-flash", Provider: "Google Antigravity", Name: "Gemini 3.8 Flash (Antigravity)", IntegrationType: "acp_agent", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "antigravity-gemini-3.6-flash", Provider: "Google Antigravity", Name: "Gemini 3.6 Flash (Antigravity)", IntegrationType: "acp_agent", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "gpt-4o", Provider: "OpenAI", Name: "GPT-4o", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "claude-3-7-sonnet", Provider: "Anthropic", Name: "Claude 3.7 Sonnet", IntegrationType: "direct_api", IsAvailable: true})

	return r
}

func (r *AIRouter) RegisterModel(model AIModel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[model.ID] = model
}

func (r *AIRouter) RegisterProvider(providerName string, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[strings.ToLower(providerName)] = provider
}

func (r *AIRouter) GetProvider(providerName string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, found := r.providers[strings.ToLower(providerName)]
	return p, found
}

func (r *AIRouter) ListAvailableModels() []AIModel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]AIModel, 0, len(r.models))
	for _, m := range r.models {
		if m.IsAvailable {
			list = append(list, m)
		}
	}
	return list
}

func (r *AIRouter) RoutePrompt(modelID, prompt string) (string, error) {
	r.mu.RLock()
	model, found := r.models[modelID]
	r.mu.RUnlock()

	if !found {
		return "", errors.New("model not found in registry")
	}

	if !model.IsAvailable {
		return "", errors.New("requested model is currently unavailable")
	}

	return model.Provider, nil
}

// Generate routes a full generation request to the registered provider for modelID.
func (r *AIRouter) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	r.mu.RLock()
	model, found := r.models[req.Model]
	r.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("model '%s' not registered in AI router", req.Model)
	}

	r.mu.RLock()
	provider, hasProvider := r.providers[strings.ToLower(model.Provider)]
	if !hasProvider {
		provider, hasProvider = r.providers["gemini api"]
	}
	r.mu.RUnlock()

	if !hasProvider || provider == nil {
		return nil, fmt.Errorf("no provider registered for '%s'", model.Provider)
	}

	return provider.Generate(ctx, req)
}
