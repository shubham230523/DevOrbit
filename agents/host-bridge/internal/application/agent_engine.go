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

var ErrUnsupportedValueType = errors.New("unsupported payload value type: must be JSON-compatible primitive, map, or slice")

func validateAndCopyValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case map[string]any:
		return validateAndCopyMap(val)
	case map[string]string:
		cp := make(map[string]string, len(val))
		for k, elem := range val {
			cp[k] = elem
		}
		return cp, nil
	case map[string]int:
		cp := make(map[string]int, len(val))
		for k, elem := range val {
			cp[k] = elem
		}
		return cp, nil
	case map[string]bool:
		cp := make(map[string]bool, len(val))
		for k, elem := range val {
			cp[k] = elem
		}
		return cp, nil
	case []any:
		cp := make([]any, len(val))
		for i, elem := range val {
			copiedElem, err := validateAndCopyValue(elem)
			if err != nil {
				return nil, err
			}
			cp[i] = copiedElem
		}
		return cp, nil
	case []string:
		return append([]string(nil), val...), nil
	case []int:
		return append([]int(nil), val...), nil
	case []int64:
		return append([]int64(nil), val...), nil
	case []float64:
		return append([]float64(nil), val...), nil
	case []bool:
		return append([]bool(nil), val...), nil
	case []router.FunctionCall:
		return deepCopyFunctionCalls(val)
	case router.FunctionCall:
		return deepCopyFunctionCall(val)
	case *ApprovalProposal:
		if val == nil {
			return nil, nil
		}
		argsCopy := append([]string(nil), val.Args...)
		return &ApprovalProposal{
			Token:          val.Token,
			TaskID:         val.TaskID,
			WorkspaceID:    val.WorkspaceID,
			ToolName:       val.ToolName,
			TargetPath:     val.TargetPath,
			ExpectedSHA256: val.ExpectedSHA256,
			Command:        val.Command,
			Args:           argsCopy,
			TargetContent:  val.TargetContent,
			Replacement:    val.Replacement,
			CreatedAt:      val.CreatedAt,
			ExpiresAt:      val.ExpiresAt,
		}, nil
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return val, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedValueType, v)
	}
}

func validateAndCopyMap(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		copiedVal, err := validateAndCopyValue(v)
		if err != nil {
			return nil, fmt.Errorf("key '%s': %w", k, err)
		}
		cp[k] = copiedVal
	}
	return cp, nil
}

func deepCopyMap(m map[string]any) (map[string]any, error) {
	return validateAndCopyMap(m)
}

func deepCopyValue(v any) (any, error) {
	return validateAndCopyValue(v)
}

func deepCopyFunctionCall(fc router.FunctionCall) (router.FunctionCall, error) {
	argsCopy, err := validateAndCopyMap(fc.Arguments)
	if err != nil {
		return router.FunctionCall{}, err
	}
	return router.FunctionCall{
		Name:      fc.Name,
		Arguments: argsCopy,
	}, nil
}

func deepCopyFunctionCalls(fcs []router.FunctionCall) ([]router.FunctionCall, error) {
	if fcs == nil {
		return nil, nil
	}
	cp := make([]router.FunctionCall, len(fcs))
	for i, fc := range fcs {
		copiedFc, err := deepCopyFunctionCall(fc)
		if err != nil {
			return nil, err
		}
		cp[i] = copiedFc
	}
	return cp, nil
}

func deepCopyFunctionResponse(fr router.FunctionResponse) (router.FunctionResponse, error) {
	respCopy, err := validateAndCopyMap(fr.Response)
	if err != nil {
		return router.FunctionResponse{}, err
	}
	return router.FunctionResponse{
		Name:     fr.Name,
		Response: respCopy,
	}, nil
}

func deepCopyFunctionResponses(frs []router.FunctionResponse) ([]router.FunctionResponse, error) {
	if frs == nil {
		return nil, nil
	}
	cp := make([]router.FunctionResponse, len(frs))
	for i, fr := range frs {
		copiedFr, err := deepCopyFunctionResponse(fr)
		if err != nil {
			return nil, err
		}
		cp[i] = copiedFr
	}
	return cp, nil
}

func deepCopyMessages(msgs []router.Message) ([]router.Message, error) {
	if msgs == nil {
		return nil, nil
	}
	cp := make([]router.Message, len(msgs))
	for i, msg := range msgs {
		fcsCopy, err := deepCopyFunctionCalls(msg.FunctionCalls)
		if err != nil {
			return nil, err
		}
		frsCopy, err := deepCopyFunctionResponses(msg.FunctionResponses)
		if err != nil {
			return nil, err
		}
		cp[i] = router.Message{
			Role:              msg.Role,
			Content:           msg.Content,
			FunctionCalls:     fcsCopy,
			FunctionResponses: frsCopy,
		}
	}
	return cp, nil
}

func deepCopyEvents(evts []TaskEvent) ([]TaskEvent, error) {
	if evts == nil {
		return nil, nil
	}
	cp := make([]TaskEvent, len(evts))
	for i, evt := range evts {
		dataCopy, err := validateAndCopyMap(evt.Data)
		if err != nil {
			return nil, err
		}
		cp[i] = TaskEvent{
			TaskID:    evt.TaskID,
			Type:      evt.Type,
			Message:   evt.Message,
			Data:      dataCopy,
			Timestamp: evt.Timestamp,
		}
	}
	return cp, nil
}

// Snapshot returns a thread-safe deep copy of the AgentTask and all its nested mutable data structures.
// Ownership & Synchronization Rules:
// 1. All live task state updates MUST acquire t.mu.Lock().
// 2. External calls (AI model generation, tool execution) MUST NOT hold t.mu.
// 3. Callers receiving a Snapshot cannot mutate internal live task state.
func (t *AgentTask) Snapshot() (*AgentTask, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	messagesCopy, err := deepCopyMessages(t.Messages)
	if err != nil {
		return nil, fmt.Errorf("snapshot messages copy failed: %w", err)
	}

	eventsCopy, err := deepCopyEvents(t.Events)
	if err != nil {
		return nil, fmt.Errorf("snapshot events copy failed: %w", err)
	}

	var pendingCopy *PendingTurnState
	if t.PendingTurn != nil {
		callStatesCopy := make([]FunctionCallState, len(t.PendingTurn.CallStates))
		for i, cs := range t.PendingTurn.CallStates {
			copiedFc, err := deepCopyFunctionCall(cs.FunctionCall)
			if err != nil {
				return nil, fmt.Errorf("snapshot call state function call copy failed: %w", err)
			}
			respCopy, err := deepCopyMap(cs.Response)
			if err != nil {
				return nil, fmt.Errorf("snapshot call state response copy failed: %w", err)
			}
			callStatesCopy[i] = FunctionCallState{
				Index:        cs.Index,
				FunctionCall: copiedFc,
				Executed:     cs.Executed,
				Response:     respCopy,
				PendingToken: cs.PendingToken,
			}
		}

		turnFcsCopy, err := deepCopyFunctionCalls(t.PendingTurn.TurnResponse.FunctionCalls)
		if err != nil {
			return nil, fmt.Errorf("snapshot pending turn function calls copy failed: %w", err)
		}

		pendingCopy = &PendingTurnState{
			TurnResponse: router.GenerateResponse{
				Model:         t.PendingTurn.TurnResponse.Model,
				Text:          t.PendingTurn.TurnResponse.Text,
				FinishReason:  t.PendingTurn.TurnResponse.FinishReason,
				FunctionCalls: turnFcsCopy,
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
	}, nil
}

type ToolExecutor interface {
	ExecuteTool(ctx context.Context, taskID, workspaceID string, call *router.FunctionCall, approvalToken string) (map[string]any, *ApprovalProposal, error)
}

type GeminiAgentEngine struct {
	mu             sync.RWMutex
	aiRouter       *router.AIRouter
	toolExecutor   ToolExecutor
	approvalMgr    *ApprovalManager
	activeTasks    map[string]*AgentTask
	eventListeners map[string]func(evt TaskEvent)
	eventHook      func(evtType EventType, data map[string]any) error
}

func NewGeminiAgentEngine(
	aiRouter *router.AIRouter,
	executor ToolExecutor,
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

func (e *GeminiAgentEngine) SetEventHook(hook func(evtType EventType, data map[string]any) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventHook = hook
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

func (e *GeminiAgentEngine) emitEvent(task *AgentTask, evtType EventType, msg string, data map[string]any) error {
	e.mu.RLock()
	hook := e.eventHook
	e.mu.RUnlock()

	if hook != nil {
		if err := hook(evtType, data); err != nil {
			log.Printf("[AgentEngine] Event hook rejected event '%s': %v", evtType, err)
			return fmt.Errorf("event hook rejected event '%s': %w", evtType, err)
		}
	}

	dataCopy, err := validateAndCopyMap(data)
	if err != nil {
		log.Printf("[AgentEngine] Payload validation error for event '%s': %v", evtType, err)
		return fmt.Errorf("cannot emit event '%s': %w", evtType, err)
	}

	evt := TaskEvent{
		TaskID:    task.ID,
		Type:      evtType,
		Message:   msg,
		Data:      dataCopy,
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
		listenerDataCopy, err := validateAndCopyMap(evt.Data)
		if err != nil {
			log.Printf("[AgentEngine] Listener payload copy warning for event '%s': %v", evtType, err)
			continue
		}
		l(TaskEvent{
			TaskID:    evt.TaskID,
			Type:      evt.Type,
			Message:   evt.Message,
			Data:      listenerDataCopy,
			Timestamp: evt.Timestamp,
		})
	}
	return nil
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
		callCopy, err := deepCopyFunctionCall(cs.FunctionCall)
		if err != nil {
			task.mu.Unlock()
			return false, fmt.Errorf("failed to copy function call arguments for tool '%s': %w", cs.FunctionCall.Name, err)
		}
		task.mu.Unlock()

		if err := e.emitEvent(task, EventToolExecutionStarted, fmt.Sprintf("Executing tool '%s' (%d/%d)", callCopy.Name, i+1, len(pt.CallStates)), map[string]any{
			"tool": callCopy.Name,
			"args": callCopy.Arguments,
		}); err != nil {
			log.Printf("[AgentEngine] Event emission warning: %v", err)
		}

		// Execute tool outside task mutex
		resMap, proposal, err := e.toolExecutor.ExecuteTool(ctx, task.ID, task.WorkspaceID, &callCopy, tok)

		task.mu.Lock()
		if err != nil {
			if errors.Is(err, ErrApprovalRequired) && proposal != nil {
				cs.PendingToken = proposal.Token
				task.Status = "approval_required"
				task.ApprovalToken = proposal.Token
				task.mu.Unlock()

				if errEvt := e.emitEvent(task, EventApprovalRequired, fmt.Sprintf("Tool '%s' requires user approval", callCopy.Name), map[string]any{
					"proposal": proposal,
				}); errEvt != nil {
					log.Printf("[AgentEngine] Critical: Failed to emit EventApprovalRequired for tool '%s': %v", callCopy.Name, errEvt)
					task.mu.Lock()
					task.Status = "failed"
					task.ErrorText = fmt.Sprintf("failed to emit EventApprovalRequired for tool '%s': %v", callCopy.Name, errEvt)
					task.ApprovalToken = ""
					task.mu.Unlock()
					return false, fmt.Errorf("failed to emit EventApprovalRequired for tool '%s': %w", callCopy.Name, errEvt)
				}
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

		if errEvt := e.emitEvent(task, EventToolExecutionCompleted, fmt.Sprintf("Tool '%s' completed", callCopy.Name), map[string]any{
			"tool":   callCopy.Name,
			"result": resMap,
		}); errEvt != nil {
			log.Printf("[AgentEngine] Event emission warning: %v", errEvt)
		}

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

	if errEvt := e.emitEvent(task, EventTaskStarted, fmt.Sprintf("Task '%s' started", taskID), map[string]any{
		"workspace_id": task.WorkspaceID,
		"model_id":     task.ModelID,
	}); errEvt != nil {
		log.Printf("[AgentEngine] Event emission warning: %v", errEvt)
	}

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
			if errEvt := e.emitEvent(task, EventTaskCancelled, "Task cancelled or timed out", nil); errEvt != nil {
				log.Printf("[AgentEngine] Critical: Failed to emit EventTaskCancelled: %v", errEvt)
				return fmt.Errorf("%w (event emission failed: %v)", taskCtx.Err(), errEvt)
			}
			return taskCtx.Err()
		default:
		}

		if errEvt := e.emitEvent(task, EventGeminiProcessing, fmt.Sprintf("Turn %d: Consulting Gemini model %s", turn, task.ModelID), nil); errEvt != nil {
			log.Printf("[AgentEngine] Observability event emission warning for '%s': %v", EventGeminiProcessing, errEvt)
		}

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
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || taskCtx.Err() != nil {
				task.mu.Lock()
				task.Status = "cancelled"
				task.ErrorText = taskCtx.Err().Error()
				task.mu.Unlock()
				if errEvt := e.emitEvent(task, EventTaskCancelled, "Task cancelled or timed out", nil); errEvt != nil {
					log.Printf("[AgentEngine] Critical: Failed to emit EventTaskCancelled: %v", errEvt)
					return fmt.Errorf("%w (event emission failed: %v)", taskCtx.Err(), errEvt)
				}
				return taskCtx.Err()
			}

			task.mu.Lock()
			task.Status = "failed"
			task.ErrorText = err.Error()
			task.mu.Unlock()
			if errEvt := e.emitEvent(task, EventTaskFailed, fmt.Sprintf("Gemini generation failed: %v", err), map[string]any{"error": err.Error()}); errEvt != nil {
				log.Printf("[AgentEngine] Critical: Failed to emit EventTaskFailed: %v", errEvt)
				return fmt.Errorf("%w (event emission failed: %v)", err, errEvt)
			}
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

			if errEvt := e.emitEvent(task, EventTaskCompleted, "Task completed successfully", map[string]any{
				"result_text": genResp.Text,
			}); errEvt != nil {
				log.Printf("[AgentEngine] Critical: Failed to emit EventTaskCompleted: %v", errEvt)
				task.mu.Lock()
				task.Status = "failed"
				task.ErrorText = fmt.Sprintf("task completed but lifecycle event emission failed: %v", errEvt)
				task.mu.Unlock()
				return fmt.Errorf("task completed but lifecycle event emission failed: %w", errEvt)
			}
			return nil
		}

		if errEvt := e.emitEvent(task, EventToolRequested, fmt.Sprintf("Gemini requested %d tool calls", len(genResp.FunctionCalls)), map[string]any{
			"function_calls": genResp.FunctionCalls,
		}); errEvt != nil {
			log.Printf("[AgentEngine] Event emission warning: %v", errEvt)
		}

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

	if errEvt := e.emitEvent(task, EventTaskCancelled, fmt.Sprintf("Task '%s' cancelled by user", taskID), nil); errEvt != nil {
		log.Printf("[AgentEngine] Event emission warning: %v", errEvt)
	}
	return true
}

func (e *GeminiAgentEngine) GetTask(taskID string) (*AgentTask, bool) {
	e.mu.RLock()
	task, found := e.activeTasks[taskID]
	e.mu.RUnlock()

	if !found {
		return nil, false
	}
	snap, err := task.Snapshot()
	if err != nil {
		log.Printf("[AgentEngine] GetTask snapshot error for '%s': %v", taskID, err)
		return nil, false
	}
	return snap, true
}
