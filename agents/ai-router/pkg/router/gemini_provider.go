package router

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"
)

// GeminiConfig holds settings for the real Gemini API provider.
type GeminiConfig struct {
	APIKey       string
	DefaultModel string
	BaseURL      string
	HTTPClient   *http.Client
}

// GeminiProvider connects directly to the Google Gen AI API using the official Go SDK.
type GeminiProvider struct {
	apiKey       string
	defaultModel string
	client       *genai.Client
}

// NewGeminiProvider creates a GeminiProvider with explicit configuration.
func NewGeminiProvider(ctx context.Context, cfg GeminiConfig) (*GeminiProvider, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEVORBIT_GEMINI_API_KEY"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	}
	if apiKey == "" {
		return nil, ErrMissingAPIKey
	}

	model := strings.TrimSpace(cfg.DefaultModel)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("DEVORBIT_GEMINI_MODEL"))
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}

	clientOpts := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if cfg.HTTPClient != nil {
		clientOpts.HTTPClient = cfg.HTTPClient
	}

	client, err := genai.NewClient(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google GenAI client: %s", RedactSecret(err.Error(), apiKey))
	}

	return &GeminiProvider{
		apiKey:       apiKey,
		defaultModel: model,
		client:       client,
	}, nil
}

func (p *GeminiProvider) Name() string {
	return "Google Gemini API"
}

// DefaultModel returns the configured default model identifier.
func (p *GeminiProvider) DefaultModel() string {
	return p.defaultModel
}

// Generate sends user prompts, history, system instructions, and tool definitions to the Gemini API.
func (p *GeminiProvider) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	if len(req.Messages) == 0 {
		return nil, ErrEmptyPrompt
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.defaultModel
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Convert messages to GenAI contents
	var contents []*genai.Content
	for _, msg := range req.Messages {
		role := strings.ToLower(msg.Role)
		if role == "assistant" {
			role = "model"
		}

		var parts []*genai.Part

		if msg.Content != "" {
			parts = append(parts, genai.NewPartFromText(msg.Content))
		}

		for _, fc := range msg.FunctionCalls {
			parts = append(parts, genai.NewPartFromFunctionCall(fc.Name, fc.Arguments))
		}

		for _, fr := range msg.FunctionResponses {
			parts = append(parts, genai.NewPartFromFunctionResponse(fr.Name, fr.Response))
		}

		if len(parts) > 0 {
			contents = append(contents, &genai.Content{
				Role:  role,
				Parts: parts,
			})
		}
	}

	config := &genai.GenerateContentConfig{}

	if strings.TrimSpace(req.SystemInstruction) != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(req.SystemInstruction)},
		}
	}

	// Add tools if provided
	if len(req.Tools) > 0 {
		var funcDecls []*genai.FunctionDeclaration
		for _, t := range req.Tools {
			decl := &genai.FunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
			}
			if len(t.Parameters) > 0 || len(t.Required) > 0 {
				props := make(map[string]*genai.Schema)
				for k, v := range t.Parameters {
					s := &genai.Schema{
						Description: v.Description,
					}
					switch v.Type {
					case "string":
						s.Type = genai.TypeString
					case "boolean":
						s.Type = genai.TypeBoolean
					case "integer":
						s.Type = genai.TypeInteger
					case "number":
						s.Type = genai.TypeNumber
					case "array":
						s.Type = genai.TypeArray
						if v.Items != nil {
							s.Items = &genai.Schema{Type: genai.TypeString}
						}
					default:
						s.Type = genai.TypeString
					}
					props[k] = s
				}
				decl.Parameters = &genai.Schema{
					Type:       genai.TypeObject,
					Properties: props,
					Required:   t.Required,
				}
			}
			funcDecls = append(funcDecls, decl)
		}
		config.Tools = []*genai.Tool{
			{FunctionDeclarations: funcDecls},
		}
	}

	result, err := p.client.Models.GenerateContent(callCtx, model, contents, config)
	if err != nil {
		redactedErrMsg := RedactSecret(err.Error(), p.apiKey)
		if callCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%w: %s", ErrRequestTimeout, redactedErrMsg)
		}
		return nil, fmt.Errorf("%w: %s", ErrProviderFailed, redactedErrMsg)
	}

	resp := &GenerateResponse{
		Model: model,
	}

	if result != nil && len(result.Candidates) > 0 {
		cand := result.Candidates[0]
		if cand.FinishReason != "" {
			resp.FinishReason = string(cand.FinishReason)
		}

		if cand.Content != nil {
			var textParts []string
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
				if part.FunctionCall != nil {
					fc := FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: part.FunctionCall.Args,
					}
					resp.FunctionCalls = append(resp.FunctionCalls, fc)
				}
			}
			resp.Text = strings.Join(textParts, "\n")
		}
	}

	return resp, nil
}
