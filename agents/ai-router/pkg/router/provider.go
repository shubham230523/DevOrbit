package router

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMissingAPIKey   = errors.New("gemini provider: API key is required and cannot be empty")
	ErrEmptyPrompt     = errors.New("gemini provider: prompt cannot be empty")
	ErrRequestTimeout  = errors.New("gemini provider: request timed out")
	ErrProviderFailed  = errors.New("gemini provider: API request failed")
)

// Message represents a conversation turn.
type Message struct {
	Role             string            `json:"role"` // "user", "model", "tool"
	Content          string            `json:"content,omitempty"`
	FunctionCalls    []FunctionCall    `json:"function_calls,omitempty"`
	FunctionResponses []FunctionResponse `json:"function_responses,omitempty"`
}

// GenerateRequest represents a generation request sent to an AI provider.
type GenerateRequest struct {
	Model             string            `json:"model"`
	SystemInstruction string            `json:"system_instruction,omitempty"`
	Messages          []Message         `json:"messages"`
	Tools             []ToolDeclaration `json:"tools,omitempty"`
	Timeout           time.Duration     `json:"timeout,omitempty"`
}

// GenerateResponse represents the unified response from an AI provider.
type GenerateResponse struct {
	Text          string         `json:"text,omitempty"`
	FunctionCalls []FunctionCall `json:"function_calls,omitempty"`
	FinishReason  string         `json:"finish_reason,omitempty"`
	Model         string         `json:"model"`
}

// Provider defines the interface for an AI backend (Gemini, mock, etc.).
type Provider interface {
	Name() string
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}
