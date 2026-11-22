package router

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

var (
	ErrModelNotFound       = errors.New("model not registered in AI router")
	ErrModelUnavailable    = errors.New("requested model is currently unavailable")
	ErrProviderUnavailable = errors.New("503 Service Unavailable: GEMINI_API_KEY is not configured on the host")
)

type AIModel struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	IntegrationType string `json:"integration_type"` // "acp_agent" or "direct_api"
	IsAvailable     bool   `json:"is_available"`
}

type AIRouterOptions struct {
	UseMockProvider bool
	GeminiAPIKey    string
	Environment     string
}

type AIRouter struct {
	mu          sync.RWMutex
	models      map[string]AIModel
	providers   map[string]Provider
	environment string
}

func NewAIRouter() *AIRouter {
	env := strings.ToLower(os.Getenv("DEVORBIT_ENV"))
	if env == "" {
		env = "development"
	}

	useMock := strings.EqualFold(os.Getenv("DEVORBIT_GEMINI_USE_MOCK"), "true")
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		geminiKey = os.Getenv("DEVORBIT_GEMINI_API_KEY")
	}

	return NewAIRouterWithOptions(AIRouterOptions{
		UseMockProvider: useMock,
		GeminiAPIKey:    geminiKey,
		Environment:     env,
	})
}

func NewAIRouterWithOptions(opts AIRouterOptions) *AIRouter {
	r := &AIRouter{
		models:      make(map[string]AIModel),
		providers:   make(map[string]Provider),
		environment: opts.Environment,
	}

	r.RegisterModel(AIModel{ID: "gemini-2.5-flash", Provider: "Gemini API", Name: "Gemini 2.5 Flash", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "gemini-2.5-pro", Provider: "Gemini API", Name: "Gemini 2.5 Pro", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "antigravity-gemini-3.8-flash", Provider: "Google Antigravity", Name: "Gemini 3.8 Flash (Antigravity)", IntegrationType: "acp_agent", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "antigravity-gemini-3.6-flash", Provider: "Google Antigravity", Name: "Gemini 3.6 Flash (Antigravity)", IntegrationType: "acp_agent", IsAvailable: true})

	if opts.UseMockProvider {
		log.Println("AI Router Warning: Using MockProvider as explicitly requested by DEVORBIT_GEMINI_USE_MOCK=true")
		mockP := NewMockGeminiProvider()
		r.RegisterProvider("Gemini API", mockP)
		r.RegisterProvider("Google Antigravity", mockP)
	} else if opts.GeminiAPIKey != "" {
		geminiP, err := NewGeminiProvider(context.Background(), GeminiConfig{APIKey: opts.GeminiAPIKey})
		if err == nil {
			r.RegisterProvider("Gemini API", geminiP)
			r.RegisterProvider("Google Antigravity", geminiP)
		} else {
			log.Printf("AI Router Warning: Failed to initialize GeminiProvider: %v", err)
		}
	} else if opts.Environment != "production" {
		log.Println("AI Router Notice: No GEMINI_API_KEY provided in development mode.")
	}

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
		return "", ErrModelNotFound
	}

	if !model.IsAvailable {
		return "", ErrModelUnavailable
	}

	return model.Provider, nil
}

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
		return nil, ErrProviderUnavailable
	}

	return provider.Generate(ctx, req)
}
