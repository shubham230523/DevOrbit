package router

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MockGeminiProvider implements the Provider interface for deterministic unit and integration tests.
type MockGeminiProvider struct {
	mu           sync.Mutex
	Responses    map[string]*GenerateResponse // Keyed by prompt or trigger substring
	DefaultResp  *GenerateResponse
	LastRequest  *GenerateRequest
	RequestCount int
	SimulatedErr error
	Delay        time.Duration
}

func NewMockGeminiProvider() *MockGeminiProvider {
	return &MockGeminiProvider{
		Responses: make(map[string]*GenerateResponse),
		DefaultResp: &GenerateResponse{
			Text:  "Hello from Mock Gemini Provider",
			Model: "gemini-2.5-flash",
		},
	}
}

func (m *MockGeminiProvider) Name() string {
	return "Mock Gemini Provider"
}

func (m *MockGeminiProvider) SetResponseForPrompt(trigger string, resp *GenerateResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Responses[trigger] = resp
}

func (m *MockGeminiProvider) SetSimulatedError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SimulatedErr = err
}

func (m *MockGeminiProvider) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	m.mu.Lock()
	m.RequestCount++
	reqCopy := req
	m.LastRequest = &reqCopy
	simErr := m.SimulatedErr
	delay := m.Delay
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if simErr != nil {
		return nil, simErr
	}

	if len(req.Messages) == 0 {
		return nil, ErrEmptyPrompt
	}

	lastMsg := req.Messages[len(req.Messages)-1]

	// Check if this is a tool response continuation
	if len(lastMsg.FunctionResponses) > 0 {
		toolName := lastMsg.FunctionResponses[0].Name
		m.mu.Lock()
		defer m.mu.Unlock()
		for trigger, resp := range m.Responses {
			if strings.Contains(trigger, toolName) {
				return resp, nil
			}
		}
		return &GenerateResponse{
			Text:  fmt.Sprintf("Received result for %s successfully.", toolName),
			Model: req.Model,
		}, nil
	}

	// Check trigger matching in last prompt text
	m.mu.Lock()
	defer m.mu.Unlock()
	for trigger, resp := range m.Responses {
		if strings.Contains(lastMsg.Content, trigger) {
			return resp, nil
		}
	}

	if m.DefaultResp != nil {
		resp := *m.DefaultResp
		if req.Model != "" {
			resp.Model = req.Model
		}
		return &resp, nil
	}

	return &GenerateResponse{
		Text:  "Acknowledged: " + lastMsg.Content,
		Model: req.Model,
	}, nil
}
