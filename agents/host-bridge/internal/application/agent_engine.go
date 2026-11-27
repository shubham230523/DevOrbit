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

type FunctionCallState struct {
	Index        int                 `json:"index"`
	FunctionCall router.FunctionCall `json:"function_call"`
	Executed     bool                `json:"executed"`
	Response     map[string]any      `json:"response,omitempty"`
	PendingToken string              `json:"pending_token,omitempty"`
}

type PendingTurnState struct {
	TurnResponse router.GenerateResponse `json:"turn_response"`
	CallStates   []FunctionCallState     `json:"call_states"`
}

type AgentTask struct {
	ID            string            `json:"id"`
	WorkspaceID   string            `json:"workspace_id"`
	Prompt        string            `json:"prompt"`
	ModelID       string            `json:"model_id"`
	Status        string            `json:"status"` // "running", "approval_required", "completed", "failed", "cancelled"
	Messages      []router.Message  `json:"messages"`
	Events        []TaskEvent       `json:"events"`
	PendingTurn   *PendingTurnState `json:"pending_turn,omitempty"`
	ApprovalToken string            `json:"approval_token,omitempty"`
	ResultText    string            `json:"result_text,omitempty"`
	ErrorText     string            `json:"error_text,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	executing     bool
	mu            sync.Mutex
	cancelFn      context.CancelFunc
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = deepCopyValue(v)
	}
	return cp
}

func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		cp := make([]any, len(val))
		for i, elem := range val {
			cp[i] = deepCopyValue(elem)
		}
		return cp
	case []string:
		return append([]string(nil), val...)
	default:
		return v
	}
}

func deepCopyFunctionCall(fc router.FunctionCall) router.FunctionCall {
	return router.FunctionCall{
		Name:      fc.Name,
		Arguments: deepCopyMap(fc.Arguments),
	}
}

func deepCopyFunctionCalls(fcs []router.FunctionCall) []router.FunctionCall {
	if fcs == nil {
		return nil
	}
	cp := make([]router.FunctionCall, len(fcs))
	for i, fc := range fcs {
		cp[i] = deepCopyFunctionCall(fc)
	}
	return cp
}

func deepCopyFunctionResponse(fr router.FunctionResponse) router.FunctionResponse {
	return router.FunctionResponse{
		Name:     fr.Name,
		Response: deepCopyMap(fr.Response),
	}
}

func deepCopyFunctionResponses(frs []router.FunctionResponse) []router.FunctionResponse {
	if frs == nil {
		return nil
	}
	cp := make([]router.FunctionResponse, len(frs))
	for i, fr := range frs {
		cp[i] = deepCopyFunctionResponse(fr)
	}
	return cp
}

func deepCopyMessages(msgs []router.Message) []router.Message {
	if msgs == nil {
		return nil
	}
	cp := make([]router.Message, len(msgs))
	for i, msg := range msgs {
		cp[i] = router.Message{
			Role:              msg.Role,
			Content:           msg.Content,
			FunctionCalls:     deepCopyFunctionCalls(msg.FunctionCalls),
			FunctionResponses: deepCopyFunctionResponses(msg.FunctionResponses),
		}
	}
	return cp
}

func deepCopyEvents(evts []TaskEvent) []TaskEvent {
	if evts == nil {
		return nil
	}
	cp := make([]TaskEvent, len(evts))
	for i, evt := range evts {
		cp[i] = TaskEvent{
			TaskID:    evt.TaskID,
			Type:      evt.Type,
			Message:   evt.Message,
			Data:      deepCopyMap(evt.Data),
			Timestamp: evt.Timestamp,
		}
	}
	return cp
}

// Snapshot returns a thread-safe deep copy of the AgentTask and all its nested mutable data structures.
// Ownership & Synchronization Rules:
// 1. All live task state updates MUST acquire t.mu.Lock().
// 2. External calls (AI model generation, tool execution) MUST NOT hold t.mu.
// 3. Callers receiving a Snapshot cannot mutate internal live task state.
func (t *AgentTask) Snapshot() *AgentTask {
	t.mu.Lock()
	defer t.mu.Unlock()

	messagesCopy := deepCopyMessages(t.Messages)
	eventsCopy := deepCopyEvents(t.Events)

	var pendingCopy *PendingTurnState
	if t.PendingTurn != nil {
		callStatesCopy := make([]FunctionCallState, len(t.PendingTurn.CallStates))
		for i, cs := range t.PendingTurn.CallStates {
			callStatesCopy[i] = FunctionCallState{
				Index:        cs.Index,
				FunctionCall: deepCopyFunctionCall(cs.FunctionCall),
				Executed:     cs.Executed,
				Response:     deepCopyMap(cs.Response),
				PendingToken: cs.PendingToken,
			}
		}
		pendingCopy = &PendingTurnState{
			TurnResponse: router.GenerateResponse{
				Model:         t.PendingTurn.TurnResponse.Model,
				Text:          t.PendingTurn.TurnResponse.Text,
				FinishReason:  t.PendingTurn.TurnResponse.FinishReason,
				FunctionCalls: deepCopyFunctionCalls(t.PendingTurn.TurnResponse.FunctionCalls),
			},
			CallStates: callStatesCopy,
		}
	}

	return &AgentTask{
		ID:            t.ID,
		WorkspaceID:   t.WorkspaceID,
		Prompt:        t.Prompt,
		ModelID:       t.ModelID,
		Status:        t.Status,
		Messages:      messagesCopy,
		Events:        eventsCopy,
		PendingTurn:   pendingCopy,
		ApprovalToken: t.ApprovalToken,
		ResultText:    t.ResultText,
		ErrorText:     t.ErrorText,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		executing:     t.executing,
	}
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
	if len(e.activeTasks) > 200 {
		for id, t := range e.activeTasks {
			t.mu.Lock()
			st := t.Status
			t.mu.Unlock()
			if st == "completed" || st == "failed" || st == "cancelled" {
				delete(e.activeTasks, id)
				if len(e.activeTasks) <= 100 {
					break
				}
			}
		}
	}
	e.activeTasks[taskID] = task
	e.mu.Unlock()

	return task, nil
}

func (e *GeminiAgentEngine) executePendingTurn(ctx context.Context, task *AgentTask, approvalToken string) (bool, error) {
	task.mu.Lock()
	pt := task.PendingTurn
	task.mu.Unlock()

	if pt == nil {
		return true, nil
	}

	for i := range pt.CallStates {
		task.mu.Lock()
		cs := &pt.CallStates[i]
		if cs.Executed {
			task.mu.Unlock()
			continue // Already executed in an earlier attempt
		}

		// Authorization validation before calling tool executor
		if cs.PendingToken != "" {
			if approvalToken == "" {
				task.Status = "approval_required"
				task.ApprovalToken = cs.PendingToken
				task.mu.Unlock()
				return false, ErrApprovalRequired
			}
			if approvalToken != cs.PendingToken {
				task.Status = "approval_required"
				task.ApprovalToken = cs.PendingToken
				task.mu.Unlock()
				return false, ErrApprovalMismatch
			}
		} else if approvalToken != "" {
			task.Status = "approval_required"
			task.mu.Unlock()
			return false, ErrApprovalMismatch
		}

		tok := approvalToken
		callCopy := cs.FunctionCall
		task.mu.Unlock()

		e.emitEvent(task, EventToolExecutionStarted, fmt.Sprintf("Executing tool '%s' (%d/%d)", callCopy.Name, i+1, len(pt.CallStates)), map[string]any{
			"tool": callCopy.Name,
			"args": callCopy.Arguments,
		})

		// Execute tool outside task mutex
		resMap, proposal, err := e.toolExecutor.ExecuteTool(ctx, task.ID, task.WorkspaceID, &callCopy, tok)

		task.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrApprovalRequired) && proposal != nil {
				cs.PendingToken = proposal.Token
				task.Status = "approval_required"
				task.ApprovalToken = proposal.Token
				task.mu.Unlock()

				e.emitEvent(task, EventApprovalRequired, fmt.Sprintf("Tool '%s' requires user approval", callCopy.Name), map[string]any{
					"proposal": proposal,
				})
				return false, nil // Paused for approval
			}

			if errors.Is(err, ErrApprovalMismatch) || errors.Is(err, ErrApprovalTokenNotFound) || errors.Is(err, ErrApprovalExpired) {
				// Authorization error: DO NOT mark Executed. Keep task in approval_required state.
				task.Status = "approval_required"
				if cs.PendingToken != "" {
					task.ApprovalToken = cs.PendingToken
				}
				task.mu.Unlock()
				return false, err
			}

			// Non-authorization execution error (e.g., file not found, command error)
			log.Printf("Tool '%s' execution error: %v", callCopy.Name, err)
			resMap = map[string]any{"error": err.Error()}
		}

		cs.Executed = true
		cs.Response = resMap
		cs.PendingToken = ""
		task.mu.Unlock()

		e.emitEvent(task, EventToolExecutionCompleted, fmt.Sprintf("Tool '%s' completed", callCopy.Name), map[string]any{
			"tool":   callCopy.Name,
			"result": resMap,
		})

		// Reset approvalToken after consumption so subsequent unexecuted calls require their own token
		approvalToken = ""
	}

	task.mu.Lock()
	// All calls in pending turn completed! Record turn messages.
	toolResponses := make([]router.FunctionResponse, len(pt.CallStates))
	for i, cs := range pt.CallStates {
		toolResponses[i] = router.FunctionResponse{
			Name:     cs.FunctionCall.Name,
			Response: cs.Response,
		}
	}

	task.Messages = append(task.Messages, router.Message{
		Role:          "model",
		FunctionCalls: pt.TurnResponse.FunctionCalls,
	})
	task.Messages = append(task.Messages, router.Message{
		Role:              "tool",
		FunctionResponses: toolResponses,
	})
	task.PendingTurn = nil
	task.ApprovalToken = ""
	task.mu.Unlock()

	return true, nil
}

func (e *GeminiAgentEngine) RunTask(ctx context.Context, taskID string, approvalToken string) error {
	return e.runTaskInternal(ctx, taskID, approvalToken, "start")
}

func (e *GeminiAgentEngine) ResumeTaskWithApproval(ctx context.Context, taskID, approvalToken string) error {
	return e.runTaskInternal(ctx, taskID, approvalToken, "resume")
}

func (e *GeminiAgentEngine) runTaskInternal(ctx context.Context, taskID string, approvalToken string, mode string) error {
	e.mu.RLock()
	task, found := e.activeTasks[taskID]
	e.mu.RUnlock()

	if !found {
		return fmt.Errorf("task '%s' not found", taskID)
	}

	taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)

	task.mu.Lock()
	if task.executing {
		task.mu.Unlock()
		cancel()
		return errors.New("task is already running")
	}

	if mode == "resume" && task.Status != "approval_required" {
		task.mu.Unlock()
		cancel()
		return fmt.Errorf("task status is '%s', expected 'approval_required'", task.Status)
	}

	task.executing = true
	task.cancelFn = cancel
	task.Status = "running"
	task.mu.Unlock()

	defer func() {
		task.mu.Lock()
		task.executing = false
		task.mu.Unlock()
		cancel()
	}()

	e.emitEvent(task, EventTaskStarted, fmt.Sprintf("Task '%s' started", taskID), map[string]any{
		"workspace_id": task.WorkspaceID,
		"model_id":     task.ModelID,
	})

	tools := router.GetStageCToolDeclarations()
	maxTurns := 15

	// Process pending turn if task was resumed from an approval state
	completed, err := e.executePendingTurn(taskCtx, task, approvalToken)
	if err != nil || !completed {
		if err != nil && (errors.Is(err, ErrApprovalMismatch) || errors.Is(err, ErrApprovalTokenNotFound) || errors.Is(err, ErrApprovalExpired)) {
			task.mu.Lock()
			task.Status = "approval_required"
			task.mu.Unlock()
		}
		return err
	}

	for turn := 1; turn <= maxTurns; turn++ {
		select {
		case <-taskCtx.Done():
			task.mu.Lock()
			if task.Status != "cancelled" {
				task.Status = "cancelled"
			}
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

		callStates := make([]FunctionCallState, len(genResp.FunctionCalls))
		for i, fc := range genResp.FunctionCalls {
			callStates[i] = FunctionCallState{
				Index:        i,
				FunctionCall: fc,
				Executed:     false,
			}
		}

		task.mu.Lock()
		task.PendingTurn = &PendingTurnState{
			TurnResponse: *genResp,
			CallStates:   callStates,
		}
		task.mu.Unlock()

		completed, err := e.executePendingTurn(taskCtx, task, "")
		if err != nil || !completed {
			return err
		}
	}

	return fmt.Errorf("task exceeded maximum turn count (%d)", maxTurns)
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
	task, found := e.activeTasks[taskID]
	e.mu.RUnlock()

	if !found {
		return nil, false
	}
	return task.Snapshot(), true
}
