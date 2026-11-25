package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
)

type EventType string

const (
	EventTaskStarted           EventType = "task_started"
	EventGeminiProcessing      EventType = "gemini_processing"
	EventToolRequested         EventType = "tool_requested"
	EventApprovalRequired      EventType = "approval_required"
	EventToolExecutionStarted  EventType = "tool_execution_started"
	EventToolExecutionCompleted EventType = "tool_execution_completed"
	EventTaskCompleted         EventType = "task_completed"
	EventTaskFailed            EventType = "task_failed"
	EventTaskCancelled         EventType = "task_cancelled"
)

type TaskEvent struct {
	TaskID    string         `json:"task_id"`
	Type      EventType      `json:"type"`
	Message   string         `json:"message"`
	Data      map[string]any `json:"data,omitempty"`
	Timestamp int64          `json:"timestamp"`
}

type AgentTask struct {
	ID            string                  `json:"id"`
	WorkspaceID   string                  `json:"workspace_id"`
	Prompt        string                  `json:"prompt"`
	ModelID       string                  `json:"model_id"`
	Status        string                  `json:"status"` // "running", "approval_required", "completed", "failed", "cancelled"
	Messages      []router.Message        `json:"messages"`
	Events        []TaskEvent             `json:"events"`
	PendingTurn   *router.GenerateResponse `json:"pending_turn,omitempty"`
	ApprovalToken string                  `json:"approval_token,omitempty"`
	ResultText    string                  `json:"result_text,omitempty"`
	ErrorText     string                  `json:"error_text,omitempty"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
	mu            sync.Mutex
	cancelFn      context.CancelFunc
}

type GeminiAgentEngine struct {
	mu             sync.RWMutex
	aiRouter       *router.AIRouter
	toolExecutor   *AgentToolExecutor
	approvalMgr    *ApprovalManager
	activeTasks    map[string]*AgentTask
	eventListeners map[string]func(evt TaskEvent)
}

func NewGeminiAgentEngine(
	aiRouter *router.AIRouter,
	executor *AgentToolExecutor,
	approvalMgr *ApprovalManager,
) *GeminiAgentEngine {
	return &GeminiAgentEngine{
		aiRouter:       aiRouter,
		toolExecutor:   executor,
		approvalMgr:    approvalMgr,
		activeTasks:    make(map[string]*AgentTask),
		eventListeners: make(map[string]func(evt TaskEvent)),
	}
}

func (e *GeminiAgentEngine) SubscribeEvents(id string, listener func(evt TaskEvent)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventListeners[id] = listener
}

func (e *GeminiAgentEngine) UnsubscribeEvents(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.eventListeners, id)
}

func (e *GeminiAgentEngine) emitEvent(task *AgentTask, evtType EventType, msg string, data map[string]any) {
	evt := TaskEvent{
		TaskID:    task.ID,
		Type:      evtType,
		Message:   msg,
		Data:      data,
		Timestamp: time.Now().UTC().UnixMicro(),
	}

	task.mu.Lock()
	task.Events = append(task.Events, evt)
	task.UpdatedAt = time.Now().UTC()
	task.mu.Unlock()

	e.mu.RLock()
	listeners := make([]func(evt TaskEvent), 0, len(e.eventListeners))
	for _, l := range e.eventListeners {
		listeners = append(listeners, l)
	}
	e.mu.RUnlock()

	for _, l := range listeners {
		l(evt)
	}
}

func (e *GeminiAgentEngine) CreateTask(workspaceID, prompt, modelID string) (*AgentTask, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate task ID: %w", err)
	}
	taskID := fmt.Sprintf("task-%s", hex.EncodeToString(b))

	if modelID == "" {
		modelID = "gemini-2.5-flash"
	}

	now := time.Now().UTC()
	task := &AgentTask{
		ID:          taskID,
		WorkspaceID: workspaceID,
		Prompt:      prompt,
		ModelID:     modelID,
		Status:      "running",
		Messages: []router.Message{
			{Role: "user", Content: prompt},
		},
		Events:    make([]TaskEvent, 0),
		CreatedAt: now,
		UpdatedAt: now,
	}

	e.mu.Lock()
	e.activeTasks[taskID] = task
	e.mu.Unlock()

	return task, nil
}

func (e *GeminiAgentEngine) RunTask(ctx context.Context, taskID string, approvalToken string) error {
	e.mu.RLock()
	task, found := e.activeTasks[taskID]
	e.mu.RUnlock()

	if !found {
		return fmt.Errorf("task '%s' not found", taskID)
	}

	taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	task.mu.Lock()
	task.cancelFn = cancel
	task.Status = "running"
	task.mu.Unlock()

	e.emitEvent(task, EventTaskStarted, fmt.Sprintf("Task '%s' started", taskID), map[string]any{
		"workspace_id": task.WorkspaceID,
		"model_id":     task.ModelID,
	})

	tools := router.GetStageCToolDeclarations()
	maxTurns := 15

	task.mu.Lock()
	pendingTurn := task.PendingTurn
	task.mu.Unlock()

	if pendingTurn != nil && approvalToken != "" {
		e.emitEvent(task, EventToolRequested, fmt.Sprintf("Resuming pending turn with approval for %d tool calls", len(pendingTurn.FunctionCalls)), map[string]any{
			"function_calls": pendingTurn.FunctionCalls,
		})

		toolResponses := make([]router.FunctionResponse, 0, len(pendingTurn.FunctionCalls))
		for _, fc := range pendingTurn.FunctionCalls {
			e.emitEvent(task, EventToolExecutionStarted, fmt.Sprintf("Executing approved tool '%s'", fc.Name), map[string]any{
				"tool": fc.Name,
				"args": fc.Arguments,
			})

			resMap, proposal, err := e.toolExecutor.ExecuteTool(taskCtx, task.ID, task.WorkspaceID, &fc, approvalToken)
			if err != nil {
				if errors.Is(err, ErrApprovalRequired) && proposal != nil {
					task.mu.Lock()
					task.Status = "approval_required"
					task.ApprovalToken = proposal.Token
					task.PendingTurn = pendingTurn
					task.mu.Unlock()

					e.emitEvent(task, EventApprovalRequired, fmt.Sprintf("Tool '%s' requires user approval", fc.Name), map[string]any{
						"proposal": proposal,
					})
					return nil
				}

				log.Printf("Approved tool '%s' execution error: %v", fc.Name, err)
				resMap = map[string]any{"error": err.Error()}
			}

			e.emitEvent(task, EventToolExecutionCompleted, fmt.Sprintf("Approved tool '%s' completed", fc.Name), map[string]any{
				"tool":   fc.Name,
				"result": resMap,
			})

			toolResponses = append(toolResponses, router.FunctionResponse{
				Name:     fc.Name,
				Response: resMap,
			})
		}

		task.mu.Lock()
		task.Messages = append(task.Messages, router.Message{
			Role:          "model",
			FunctionCalls: pendingTurn.FunctionCalls,
		})
		task.Messages = append(task.Messages, router.Message{
			Role:              "tool",
			FunctionResponses: toolResponses,
		})
		task.PendingTurn = nil
		task.ApprovalToken = ""
		task.mu.Unlock()

		approvalToken = ""
	}

	for turn := 1; turn <= maxTurns; turn++ {
		select {
		case <-taskCtx.Done():
			task.mu.Lock()
			task.Status = "cancelled"
			task.mu.Unlock()
			e.emitEvent(task, EventTaskCancelled, "Task cancelled or timed out", nil)
			return taskCtx.Err()
		default:
		}

		e.emitEvent(task, EventGeminiProcessing, fmt.Sprintf("Turn %d: Consulting Gemini model %s", turn, task.ModelID), nil)

		task.mu.Lock()
		messagesCopy := make([]router.Message, len(task.Messages))
		copy(messagesCopy, task.Messages)
		task.mu.Unlock()

		genReq := router.GenerateRequest{
			Model:    task.ModelID,
			Messages: messagesCopy,
			Tools:    tools,
		}

		genResp, err := e.aiRouter.Generate(taskCtx, genReq)
		if err != nil {
			task.mu.Lock()
			task.Status = "failed"
			task.ErrorText = err.Error()
			task.mu.Unlock()
			e.emitEvent(task, EventTaskFailed, fmt.Sprintf("Gemini generation failed: %v", err), map[string]any{"error": err.Error()})
			return err
		}

		if len(genResp.FunctionCalls) == 0 {
			task.mu.Lock()
			task.Status = "completed"
			task.ResultText = genResp.Text
			task.Messages = append(task.Messages, router.Message{
				Role:    "model",
				Content: genResp.Text,
			})
			task.mu.Unlock()

			e.emitEvent(task, EventTaskCompleted, "Task completed successfully", map[string]any{
				"result_text": genResp.Text,
			})
			return nil
		}

		e.emitEvent(task, EventToolRequested, fmt.Sprintf("Gemini requested %d tool calls", len(genResp.FunctionCalls)), map[string]any{
			"function_calls": genResp.FunctionCalls,
		})

		toolResponses := make([]router.FunctionResponse, 0, len(genResp.FunctionCalls))

		for _, fc := range genResp.FunctionCalls {
			e.emitEvent(task, EventToolExecutionStarted, fmt.Sprintf("Executing tool '%s'", fc.Name), map[string]any{
				"tool": fc.Name,
				"args": fc.Arguments,
			})

			resMap, proposal, err := e.toolExecutor.ExecuteTool(taskCtx, task.ID, task.WorkspaceID, &fc, approvalToken)
			if err != nil {
				if errors.Is(err, ErrApprovalRequired) && proposal != nil {
					task.mu.Lock()
					task.Status = "approval_required"
					task.ApprovalToken = proposal.Token
					task.PendingTurn = genResp
					task.mu.Unlock()

					e.emitEvent(task, EventApprovalRequired, fmt.Sprintf("Tool '%s' requires user approval", fc.Name), map[string]any{
						"proposal": proposal,
					})
					return nil
				}

				log.Printf("Tool '%s' execution error: %v", fc.Name, err)
				resMap = map[string]any{"error": err.Error()}
			}

			e.emitEvent(task, EventToolExecutionCompleted, fmt.Sprintf("Tool '%s' completed", fc.Name), map[string]any{
				"tool":   fc.Name,
				"result": resMap,
			})

			toolResponses = append(toolResponses, router.FunctionResponse{
				Name:     fc.Name,
				Response: resMap,
			})
		}

		task.mu.Lock()
		task.Messages = append(task.Messages, router.Message{
			Role:          "model",
			FunctionCalls: genResp.FunctionCalls,
		})
		task.Messages = append(task.Messages, router.Message{
			Role:              "tool",
			FunctionResponses: toolResponses,
		})
		task.mu.Unlock()
	}

	return fmt.Errorf("task exceeded maximum turn count (%d)", maxTurns)
}

func (e *GeminiAgentEngine) ResumeTaskWithApproval(ctx context.Context, taskID, approvalToken string) error {
	return e.RunTask(ctx, taskID, approvalToken)
}

func (e *GeminiAgentEngine) CancelTask(taskID string) bool {
	e.mu.RLock()
	task, found := e.activeTasks[taskID]
	e.mu.RUnlock()

	if !found {
		return false
	}

	task.mu.Lock()
	if task.cancelFn != nil {
		task.cancelFn()
	}
	task.Status = "cancelled"
	task.mu.Unlock()

	e.emitEvent(task, EventTaskCancelled, fmt.Sprintf("Task '%s' cancelled by user", taskID), nil)
	return true
}

func (e *GeminiAgentEngine) GetTask(taskID string) (*AgentTask, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, found := e.activeTasks[taskID]
	return t, found
}
