package router

import (
	"errors"
)

type AIModel struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	IntegrationType string `json:"integration_type"` // "acp_agent" or "direct_api"
	IsAvailable     bool   `json:"is_available"`
}

type AIRouter struct {
	models map[string]AIModel
}

func NewAIRouter() *AIRouter {
	r := &AIRouter{models: make(map[string]AIModel)}

	// Register models according to requirements
	r.RegisterModel(AIModel{ID: "antigravity-gemini-3.8-flash", Provider: "Google Antigravity", Name: "Gemini 3.8 Flash (Antigravity)", IntegrationType: "acp_agent", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "antigravity-gemini-3.6-flash", Provider: "Google Antigravity", Name: "Gemini 3.6 Flash (Antigravity)", IntegrationType: "acp_agent", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "gemini-2.5-pro", Provider: "Gemini API", Name: "Gemini 2.5 Pro", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "gpt-4o", Provider: "OpenAI", Name: "GPT-4o", IntegrationType: "direct_api", IsAvailable: true})
	r.RegisterModel(AIModel{ID: "claude-3-7-sonnet", Provider: "Anthropic", Name: "Claude 3.7 Sonnet", IntegrationType: "direct_api", IsAvailable: true})

	return r
}

func (r *AIRouter) RegisterModel(model AIModel) {
	r.models[model.ID] = model
}

func (r *AIRouter) ListAvailableModels() []AIModel {
	list := make([]AIModel, 0, len(r.models))
	for _, m := range r.models {
		if m.IsAvailable {
			list = append(list, m)
		}
	}
	return list
}

func (r *AIRouter) RoutePrompt(modelID, prompt string) (string, error) {
	model, found := r.models[modelID]
	if !found {
		return "", errors.New("model not found in registry")
	}

	if !model.IsAvailable {
		return "", errors.New("requested model is currently unavailable")
	}

	return model.Provider, nil
}
