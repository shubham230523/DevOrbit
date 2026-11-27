package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestAgentEngine_ToolExecutionAndApprovalWorkflow(t *testing.T) {
	// 1. Create temp workspace
	tempDir, err := os.MkdirTemp("", "devorbit_stage_c_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	aiRouter := router.NewAIRouterWithOptions(router.AIRouterOptions{
		UseMockProvider: true,
	})

	_ = NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	// Step A: Read-Only Tool Execution (Read File)
	testFilePath := filepath.Join(tempDir, "app_config.txt")
	_ = os.WriteFile(testFilePath, []byte("app_name: DevOrbit\nversion: 1.0"), 0644)

	readCall := &router.FunctionCall{
		Name: router.ToolReadFile,
		Arguments: map[string]any{
			"path": "app_config.txt",
		},
	}

	readRes, proposal, err := executor.ExecuteTool(context.Background(), "task-1", tempDir, readCall, "")
	if err != nil || proposal != nil {
		t.Fatalf("Read tool should execute automatically without approval, got err: %v", err)
	}
	if readRes["content"] != "app_name: DevOrbit\nversion: 1.0" {
		t.Errorf("Read tool returned incorrect content: %v", readRes["content"])
	}

	// Step B: Mutating Tool Execution without Approval -> Must return ApprovalRequired
	createCall := &router.FunctionCall{
		Name: router.ToolCreateFile,
		Arguments: map[string]any{
			"path":    "FEATURE.md",
			"content": "# Stage C Agent Engine",
		},
	}

	_, proposal, err = executor.ExecuteTool(context.Background(), "task-1", tempDir, createCall, "")
	if err != ErrApprovalRequired || proposal == nil {
		t.Fatalf("Mutating tool without token must return ErrApprovalRequired, got err: %v", err)
	}

	// Step C: Execute Mutating Tool WITH Approval Token -> Must succeed
	createRes, _, err := executor.ExecuteTool(context.Background(), "task-1", tempDir, createCall, proposal.Token)
	if err != nil {
		t.Fatalf("Tool execution with valid approval token failed: %v", err)
	}
	if createRes["status"] != "created" {
		t.Errorf("Expected status created, got: %v", createRes["status"])
	}

	// Step D: Verify File was Actually Created inside Workspace Jail
	createdContent, err := os.ReadFile(filepath.Join(tempDir, "FEATURE.md"))
	if err != nil || string(createdContent) != "# Stage C Agent Engine" {
		t.Errorf("Created file content mismatch: %s", string(createdContent))
	}

	// Step E: Path Traversal Attack Attempt -> Must fail
	traversalCall := &router.FunctionCall{
		Name: router.ToolReadFile,
		Arguments: map[string]any{
			"path": "../../etc/passwd",
		},
	}
	_, _, err = executor.ExecuteTool(context.Background(), "task-1", tempDir, traversalCall, "")
	if err == nil {
		t.Errorf("Security flaw! Path traversal outside workspace jail was permitted")
	}
}

func TestAgentEngine_MultiTurnTaskExecution(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "devorbit_stage_c_multiturn_*")
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	mockProvider.SetResponseForPrompt("inspect workspace", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "I inspected the workspace and verified the project tree.",
	})

	engine := NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	var receivedEvents []TaskEvent
	engine.SubscribeEvents("test-listener", func(evt TaskEvent) {
		receivedEvents = append(receivedEvents, evt)
	})

	task, err := engine.CreateTask(tempDir, "inspect workspace", "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	err = engine.RunTask(context.Background(), task.ID, "")
	if err != nil {
		t.Fatalf("Task execution failed: %v", err)
	}

	taskCompleted, found := engine.GetTask(task.ID)
	if !found || taskCompleted.Status != "completed" {
		t.Errorf("Expected task status 'completed', got: %s", taskCompleted.Status)
	}

	if len(receivedEvents) == 0 {
		t.Errorf("Expected TaskEvents to be emitted during task lifecycle")
	}
}

func TestApprovalManager_ArgumentAndPayloadBindingEnforcement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_approval_binding_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	// Test 1: Command argument tampering
	safeCmdCall := &router.FunctionCall{
		Name: router.ToolExecuteCommand,
		Arguments: map[string]any{
			"command": "git",
			"args":    []any{"status"},
		},
	}
	_, proposal, err := executor.ExecuteTool(context.Background(), "task-sec-1", tempDir, safeCmdCall, "")
	if err != ErrApprovalRequired || proposal == nil {
		t.Fatalf("Expected ErrApprovalRequired, got err: %v", err)
	}

	tamperedCmdCall := &router.FunctionCall{
		Name: router.ToolExecuteCommand,
		Arguments: map[string]any{
			"command": "git",
			"args":    []any{"push", "--force"},
		},
	}
	_, _, err = executor.ExecuteTool(context.Background(), "task-sec-1", tempDir, tamperedCmdCall, proposal.Token)
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Tampered command arguments were permitted under approved token, got err: %v", err)
	}

	// Test 2: File content payload tampering
	safeCreateCall := &router.FunctionCall{
		Name: router.ToolCreateFile,
		Arguments: map[string]any{
			"path":    "safe.txt",
			"content": "safe content",
		},
	}
	_, proposal2, err := executor.ExecuteTool(context.Background(), "task-sec-2", tempDir, safeCreateCall, "")
	if err != ErrApprovalRequired || proposal2 == nil {
		t.Fatalf("Expected ErrApprovalRequired, got err: %v", err)
	}

	tamperedCreateCall := &router.FunctionCall{
		Name: router.ToolCreateFile,
		Arguments: map[string]any{
			"path":    "safe.txt",
			"content": "MALICIOUS CONTENT",
		},
	}
	_, _, err = executor.ExecuteTool(context.Background(), "task-sec-2", tempDir, tamperedCreateCall, proposal2.Token)
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Tampered file content was permitted under approved token, got err: %v", err)
	}
}

func TestApprovalManager_ExhaustiveAdversarialBinding(t *testing.T) {
	mgr := NewApprovalManager()

	// 1. Setup baseline proposal
	prop, err := mgr.CreateProposal(
		"task-100", "ws-100", "edit_file", "src/main.kt", "hash-12345",
		"old_code()", "new_code()", "", nil, "reason", 10*time.Minute,
	)
	if err != nil {
		t.Fatalf("Failed to create proposal: %v", err)
	}

	// Case A: Non-empty target content replaced with empty content -> MUST FAIL
	_, err = mgr.ConsumeApproval(prop.Token, "task-100", "ws-100", "edit_file", "src/main.kt", "hash-12345", "", nil, "", "new_code()")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Non-empty approved targetContent replaced with empty string passed, got: %v", err)
	}

	// Case B: Non-empty replacement replaced with empty replacement -> MUST FAIL
	_, err = mgr.ConsumeApproval(prop.Token, "task-100", "ws-100", "edit_file", "src/main.kt", "hash-12345", "", nil, "old_code()", "")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Non-empty approved replacement replaced with empty string passed, got: %v", err)
	}

	// Case C: Altered file path -> MUST FAIL
	_, err = mgr.ConsumeApproval(prop.Token, "task-100", "ws-100", "edit_file", "src/OTHER.kt", "hash-12345", "", nil, "old_code()", "new_code()")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Altered file path passed, got: %v", err)
	}

	// Case D: Altered original file hash -> MUST FAIL
	_, err = mgr.ConsumeApproval(prop.Token, "task-100", "ws-100", "edit_file", "src/main.kt", "hash-WRONG", "", nil, "old_code()", "new_code()")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Altered expected SHA256 passed, got: %v", err)
	}

	// Case E: Different task ID -> MUST FAIL
	_, err = mgr.ConsumeApproval(prop.Token, "task-ATTACKER", "ws-100", "edit_file", "src/main.kt", "hash-12345", "", nil, "old_code()", "new_code()")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Cross-task approval reuse passed, got: %v", err)
	}

	// Case F: Different workspace ID -> MUST FAIL
	_, err = mgr.ConsumeApproval(prop.Token, "task-100", "ws-ATTACKER", "edit_file", "src/main.kt", "hash-12345", "", nil, "old_code()", "new_code()")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Cross-workspace approval reuse passed, got: %v", err)
	}

	// Setup command proposal
	cmdProp, _ := mgr.CreateProposal(
		"task-200", "ws-200", "execute_command", "", "",
		"", "", "git", []string{"status"}, "reason", 10*time.Minute,
	)

	// Case G: Non-empty command replaced with empty command -> MUST FAIL
	_, err = mgr.ConsumeApproval(cmdProp.Token, "task-200", "ws-200", "execute_command", "", "", "", []string{"status"}, "", "")
	if err != ErrApprovalMismatch {
		t.Errorf("Security flaw! Non-empty approved command replaced with empty string passed, got: %v", err)
	}

	// Case H: Single-use enforcement & Token reuse -> First consume succeeds, second MUST FAIL
	pValid, err := mgr.ConsumeApproval(cmdProp.Token, "task-200", "ws-200", "execute_command", "", "", "git", []string{"status"}, "", "")
	if err != nil || pValid == nil {
		t.Fatalf("Valid token consumption failed: %v", err)
	}

	_, err = mgr.ConsumeApproval(cmdProp.Token, "task-200", "ws-200", "execute_command", "", "", "git", []string{"status"}, "", "")
	if err != ErrApprovalTokenNotFound {
		t.Errorf("Security flaw! Token reuse permitted, got: %v", err)
	}

	// Case I: Concurrent token consumption -> Only 1 caller succeeds
	concurrentProp, _ := mgr.CreateProposal(
		"task-300", "ws-300", "delete_file", "temp.txt", "",
		"", "", "", nil, "reason", 10*time.Minute,
	)

	successCount := 0
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cErr := mgr.ConsumeApproval(concurrentProp.Token, "task-300", "ws-300", "delete_file", "temp.txt", "", "", nil, "", "")
			if cErr == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("Security flaw! Concurrent consumption permitted %d successes, expected exactly 1", successCount)
	}
}

func TestAgentEngine_PendingTurnResumptionState(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_pending_turn_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	_ = os.WriteFile(filepath.Join(tempDir, "existing.txt"), []byte("existing content"), 0644)

	// Mock response with 2 function calls:
	// Call 0: read_file (read-only -> executes immediately)
	// Call 1: create_file (mutating -> requires approval)
	mockProvider.SetResponseForPrompt("multi tool test", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "",
		FunctionCalls: []router.FunctionCall{
			{
				Name: router.ToolReadFile,
				Arguments: map[string]any{
					"path": "existing.txt",
				},
			},
			{
				Name: router.ToolCreateFile,
				Arguments: map[string]any{
					"path":    "created_by_agent.txt",
					"content": "hello from agent",
				},
			},
		},
	})

	engine := NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	task, err := engine.CreateTask(tempDir, "multi tool test", "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// 1. Initial execution pause on Call 1 (create_file approval required)
	err = engine.RunTask(context.Background(), task.ID, "")
	if err != nil {
		t.Fatalf("RunTask returned unexpected error: %v", err)
	}

	taskState, _ := engine.GetTask(task.ID)
	if taskState.Status != "approval_required" || taskState.ApprovalToken == "" {
		t.Fatalf("Expected status 'approval_required', got: %s", taskState.Status)
	}

	// Verify Call 0 (read_file) was executed and recorded in CallStates[0]
	if taskState.PendingTurn == nil || len(taskState.PendingTurn.CallStates) != 2 {
		t.Fatalf("Expected 2 call states in PendingTurn, got: %v", taskState.PendingTurn)
	}
	if !taskState.PendingTurn.CallStates[0].Executed {
		t.Errorf("Call 0 (read_file) should have been marked Executed = true before pausing")
	}

	// 2. Test status validation on non-approval state
	engine.mu.RLock()
	activeT := engine.activeTasks[task.ID]
	engine.mu.RUnlock()

	activeT.mu.Lock()
	activeT.Status = "completed"
	activeT.mu.Unlock()

	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, taskState.ApprovalToken)
	if err == nil || !strings.Contains(err.Error(), "expected 'approval_required'") {
		t.Errorf("Expected 'expected approval_required' error for invalid status, got: %v", err)
	}

	activeT.mu.Lock()
	activeT.Status = "approval_required"
	activeT.mu.Unlock()

	// 3. Resume task with valid approval token -> Call 0 MUST NOT be re-executed, Call 1 MUST execute
	token := taskState.ApprovalToken
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, token)
	if err != nil {
		t.Fatalf("ResumeTaskWithApproval failed: %v", err)
	}

	taskFinal, _ := engine.GetTask(task.ID)
	if taskFinal.Status != "completed" {
		t.Errorf("Expected final task status 'completed', got: %s", taskFinal.Status)
	}

	// Verify created_by_agent.txt was created
	createdData, err := os.ReadFile(filepath.Join(tempDir, "created_by_agent.txt"))
	if err != nil || string(createdData) != "hello from agent" {
		t.Errorf("File creation failed during resumed turn execution: %v", err)
	}
}

func TestAgentEngine_AdversarialAuthorizationAndMultiplePauseResumes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_adv_resumes_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	// Mock turn requiring 2 sequential approval-required tool calls:
	// Call 0: create_file("fileA.txt")
	// Call 1: create_file("fileB.txt")
	mockProvider.SetResponseForPrompt("two approvals test", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "Created fileA and fileB",
		FunctionCalls: []router.FunctionCall{
			{
				Name: router.ToolCreateFile,
				Arguments: map[string]any{
					"path":    "fileA.txt",
					"content": "content A",
				},
			},
			{
				Name: router.ToolCreateFile,
				Arguments: map[string]any{
					"path":    "fileB.txt",
					"content": "content B",
				},
			},
		},
	})

	engine := NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	task, err := engine.CreateTask(tempDir, "two approvals test", "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Step 1: Initial run -> Pauses on Call 0 (fileA approval required)
	err = engine.RunTask(context.Background(), task.ID, "")
	if err != nil {
		t.Fatalf("Initial RunTask failed: %v", err)
	}

	tState, _ := engine.GetTask(task.ID)
	if tState.Status != "approval_required" {
		t.Fatalf("Expected status 'approval_required', got: %s", tState.Status)
	}
	tokenA := tState.ApprovalToken

	// Adversarial Test 1: Invalid/wrong token submission -> Must fail and keep Call 0 unexecuted
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, "appr-INVALID-TOKEN")
	if err != ErrApprovalMismatch && err != ErrApprovalTokenNotFound {
		t.Errorf("Expected authorization mismatch error for invalid token, got: %v", err)
	}

	tStateAdv1, _ := engine.GetTask(task.ID)
	if tStateAdv1.Status != "approval_required" {
		t.Errorf("Task status should remain 'approval_required' after rejected invalid token, got: %s", tStateAdv1.Status)
	}
	if tStateAdv1.PendingTurn == nil || tStateAdv1.PendingTurn.CallStates[0].Executed {
		t.Errorf("Call 0 must remain unexecuted after invalid token rejection")
	}

	// Step 2: Resume Call 0 with valid tokenA -> Executes Call 0, then pauses on Call 1!
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, tokenA)
	if err != nil {
		t.Fatalf("Resume with tokenA failed: %v", err)
	}

	tStateStep2, _ := engine.GetTask(task.ID)
	if tStateStep2.Status != "approval_required" {
		t.Fatalf("Task should pause on Call 1 ('approval_required'), got: %s", tStateStep2.Status)
	}
	tokenB := tStateStep2.ApprovalToken

	if tokenA == tokenB {
		t.Errorf("Call 1 must generate a distinct approval token from Call 0")
	}

	// Verify fileA was created and Call 0 is marked Executed
	if !tStateStep2.PendingTurn.CallStates[0].Executed || tStateStep2.PendingTurn.CallStates[1].Executed {
		t.Errorf("Call 0 should be Executed=true and Call 1 should be Executed=false")
	}
	fileABytes, _ := os.ReadFile(filepath.Join(tempDir, "fileA.txt"))
	if string(fileABytes) != "content A" {
		t.Errorf("fileA.txt content mismatch: %s", string(fileABytes))
	}

	// Adversarial Test 2: Replaying tokenA on paused Call 1 -> Must fail (tokenA already consumed)
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, tokenA)
	if err != ErrApprovalTokenNotFound && err != ErrApprovalMismatch {
		t.Errorf("Replaying consumed tokenA must fail, got: %v", err)
	}

	// Step 3: Resume Call 1 with valid tokenB -> Completes turn successfully!
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, tokenB)
	if err != nil {
		t.Fatalf("Resume with tokenB failed: %v", err)
	}

	tFinal, _ := engine.GetTask(task.ID)
	if tFinal.Status != "completed" {
		t.Errorf("Task should be 'completed', got: %s", tFinal.Status)
	}

	fileBBytes, _ := os.ReadFile(filepath.Join(tempDir, "fileB.txt"))
	if string(fileBBytes) != "content B" {
		t.Errorf("fileB.txt content mismatch: %s", string(fileBBytes))
	}
}

type BlockingAIProvider struct {
	router.Provider
	enteredChan chan struct{}
	blockChan   chan struct{}
}

func (p *BlockingAIProvider) Name() string { return "Gemini API" }
func (p *BlockingAIProvider) DefaultModel() string { return "gemini-2.5-flash" }
func (p *BlockingAIProvider) Generate(ctx context.Context, req router.GenerateRequest) (*router.GenerateResponse, error) {
	if p.enteredChan != nil {
		select {
		case p.enteredChan <- struct{}{}:
		default:
		}
	}
	if p.blockChan != nil {
		select {
		case <-p.blockChan:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &router.GenerateResponse{
		Model: req.Model,
		Text:  "blocking completion finished",
	}, nil
}

func TestAgentEngine_StartBarrierCompetingOwnership(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_start_barrier_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	// --- Scenario A: Two simultaneous RunTask calls competing for ownership ---
	enteredA := make(chan struct{}, 1)
	blockA := make(chan struct{})
	blockingProvA := &BlockingAIProvider{enteredChan: enteredA, blockChan: blockA}

	aiRouterA := router.NewAIRouter()
	aiRouterA.RegisterProvider("Gemini API", blockingProvA)

	engineA := NewGeminiAgentEngine(aiRouterA, executor, approvalMgr)
	taskA, _ := engineA.CreateTask(tempDir, "start barrier test A", "gemini-2.5-flash")

	readyA := make(chan struct{}, 2)
	barrierA := make(chan struct{})
	resA := make(chan error, 2)

	go func() {
		readyA <- struct{}{}
		<-barrierA
		resA <- engineA.RunTask(context.Background(), taskA.ID, "")
	}()

	go func() {
		readyA <- struct{}{}
		<-barrierA
		resA <- engineA.RunTask(context.Background(), taskA.ID, "")
	}()

	<-readyA
	<-readyA
	close(barrierA)

	// Wait for the winning call to enter Generate and hold execution ownership
	<-enteredA

	// Collect first error result (which MUST be the losing request's "task is already running" error)
	losingErrA := <-resA
	if losingErrA == nil || losingErrA.Error() != "task is already running" {
		t.Fatalf("Scenario A: expected losing request to receive 'task is already running', got: %v", losingErrA)
	}

	// Unblock the winning call
	close(blockA)

	// Collect second error result (winning request's success)
	winningErrA := <-resA
	if winningErrA != nil {
		t.Fatalf("Scenario A: expected winning request to succeed, got: %v", winningErrA)
	}

	// --- Scenario B: Two simultaneous ResumeTaskWithApproval calls competing with the SAME valid token ---
	enteredB := make(chan struct{}, 1)
	blockB := make(chan struct{})
	blockingProvB := &BlockingAIProvider{enteredChan: enteredB, blockChan: blockB}

	aiRouterB := router.NewAIRouter()
	mockProvInitB := router.NewMockGeminiProvider()
	mockProvInitB.SetResponseForPrompt("start barrier test B", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "",
		FunctionCalls: []router.FunctionCall{
			{
				Name: router.ToolCreateFile,
				Arguments: map[string]any{
					"path":    "scenB.txt",
					"content": "scen B content",
				},
			},
		},
	})
	aiRouterB.RegisterProvider("Gemini API", mockProvInitB)

	engineB := NewGeminiAgentEngine(aiRouterB, executor, approvalMgr)
	taskB, _ := engineB.CreateTask(tempDir, "start barrier test B", "gemini-2.5-flash")
	_ = engineB.RunTask(context.Background(), taskB.ID, "") // Pauses on approval

	tStateB, _ := engineB.GetTask(taskB.ID)
	tokenScenB := tStateB.ApprovalToken

	// Register blocking provider for resume turn
	aiRouterB.RegisterProvider("Gemini API", blockingProvB)

	readyB := make(chan struct{}, 2)
	barrierB := make(chan struct{})
	resB := make(chan error, 2)

	go func() {
		readyB <- struct{}{}
		<-barrierB
		resB <- engineB.ResumeTaskWithApproval(context.Background(), taskB.ID, tokenScenB)
	}()

	go func() {
		readyB <- struct{}{}
		<-barrierB
		resB <- engineB.ResumeTaskWithApproval(context.Background(), taskB.ID, tokenScenB)
	}()

	<-readyB
	<-readyB
	close(barrierB)

	<-enteredB

	losingErrB := <-resB
	if losingErrB == nil || losingErrB.Error() != "task is already running" {
		t.Fatalf("Scenario B: expected losing request to receive 'task is already running', got: %v", losingErrB)
	}

	close(blockB)

	winningErrB := <-resB
	if winningErrB != nil {
		t.Fatalf("Scenario B: expected winning request to succeed, got: %v", winningErrB)
	}

	// Verify file was created EXACTLY ONCE
	scenBData, err := os.ReadFile(filepath.Join(tempDir, "scenB.txt"))
	if err != nil || string(scenBData) != "scen B content" {
		t.Errorf("Scenario B file creation failed: %v", err)
	}

	// --- Scenario C: RunTask racing ResumeTaskWithApproval against the same task ---
	enteredC := make(chan struct{}, 1)
	blockC := make(chan struct{})
	blockingProvC := &BlockingAIProvider{enteredChan: enteredC, blockChan: blockC}

	aiRouterC := router.NewAIRouter()
	mockProvInitC := router.NewMockGeminiProvider()
	mockProvInitC.SetResponseForPrompt("start barrier test C", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "",
		FunctionCalls: []router.FunctionCall{
			{
				Name: router.ToolCreateFile,
				Arguments: map[string]any{
					"path":    "scenC.txt",
					"content": "scen C content",
				},
			},
		},
	})
	aiRouterC.RegisterProvider("Gemini API", mockProvInitC)

	engineC := NewGeminiAgentEngine(aiRouterC, executor, approvalMgr)
	taskC, _ := engineC.CreateTask(tempDir, "start barrier test C", "gemini-2.5-flash")
	_ = engineC.RunTask(context.Background(), taskC.ID, "") // Pauses on approval

	tStateC, _ := engineC.GetTask(taskC.ID)
	tokenScenC := tStateC.ApprovalToken

	aiRouterC.RegisterProvider("Gemini API", blockingProvC)

	readyC := make(chan struct{}, 2)
	barrierC := make(chan struct{})
	resC := make(chan error, 2)

	go func() {
		readyC <- struct{}{}
		<-barrierC
		resC <- engineC.RunTask(context.Background(), taskC.ID, "")
	}()

	go func() {
		readyC <- struct{}{}
		<-barrierC
		resC <- engineC.ResumeTaskWithApproval(context.Background(), taskC.ID, tokenScenC)
	}()

	<-readyC
	<-readyC
	close(barrierC)

	<-enteredC

	losingErrC := <-resC
	if losingErrC == nil || (!strings.Contains(losingErrC.Error(), "already running") && !errors.Is(losingErrC, ErrApprovalRequired) && !strings.Contains(losingErrC.Error(), "expected 'approval_required'")) {
		t.Fatalf("Scenario C: expected losing request to be rejected, got: %v", losingErrC)
	}

	close(blockC)

	winningErrC := <-resC
	if winningErrC != nil {
		t.Fatalf("Scenario C: expected winning request to succeed, got: %v", winningErrC)
	}
}

func TestAgentEngine_DeepSnapshotIsolationAndMutationImmunity(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_snapshot_isolation_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	mockProvider.SetResponseForPrompt("snapshot test", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "",
		FunctionCalls: []router.FunctionCall{
			{
				Name: router.ToolCreateFile,
				Arguments: map[string]any{
					"path":    "orig.txt",
					"content": "orig content",
					"nested_map": map[string]any{
						"str_key": "orig_str",
					},
					"nested_slice": []any{"item1", "item2"},
				},
			},
		},
	})

	engine := NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	task, _ := engine.CreateTask(tempDir, "snapshot test", "gemini-2.5-flash")
	_ = engine.RunTask(context.Background(), task.ID, "")

	// Obtain snapshot
	snap, found := engine.GetTask(task.ID)
	if !found || snap == nil || snap.PendingTurn == nil {
		t.Fatalf("Failed to obtain snapshot with PendingTurn")
	}

	// Mutate nested snapshot data structures aggressively
	snap.Events[0].Data["workspace_id"] = "MUTATED_WORKSPACE"
	snap.PendingTurn.TurnResponse.FunctionCalls[0].Arguments["path"] = "MUTATED_PATH"
	snap.PendingTurn.CallStates[0].FunctionCall.Arguments["content"] = "MUTATED_CONTENT"

	// Fetch fresh snapshot from live task
	snapFresh, _ := engine.GetTask(task.ID)

	// Verify original live task state remains completely unmutated
	if snapFresh.Events[0].Data["workspace_id"] == "MUTATED_WORKSPACE" {
		t.Errorf("Mutation leak! Event Data map was mutated via snapshot")
	}
	if snapFresh.PendingTurn.TurnResponse.FunctionCalls[0].Arguments["path"] == "MUTATED_PATH" {
		t.Errorf("Mutation leak! FunctionCall arguments map was mutated via snapshot")
	}
	if snapFresh.PendingTurn.CallStates[0].FunctionCall.Arguments["content"] == "MUTATED_CONTENT" {
		t.Errorf("Mutation leak! CallState FunctionCall arguments map was mutated via snapshot")
	}
}

type AdversarialMutatingExecutor struct {
	mu               sync.Mutex
	call0Invocations int
	call1Invocations int
}

func (m *AdversarialMutatingExecutor) ExecuteTool(ctx context.Context, taskID, workspaceID string, call *router.FunctionCall, approvalToken string) (map[string]any, *ApprovalProposal, error) {
	m.mu.Lock()
	if call != nil {
		if call.Name == "custom_tool" {
			m.call0Invocations++
		} else if call.Name == "create_file_approval" {
			m.call1Invocations++
		}
	}
	m.mu.Unlock()

	// Aggressively mutate the received FunctionCall.Arguments through the real executor call path
	if call != nil && call.Arguments != nil {
		call.Arguments["path"] = "MUTATED_BY_EXECUTOR.txt"
		if nestedMap, ok := call.Arguments["nested_map"].(map[string]any); ok {
			nestedMap["key"] = "MUTATED_BY_EXECUTOR"
		}
		if nestedSlice, ok := call.Arguments["nested_slice"].([]any); ok && len(nestedSlice) > 0 {
			nestedSlice[0] = "MUTATED_BY_EXECUTOR"
		}
		call.Arguments["added_key"] = "ADDED_BY_EXECUTOR"
		delete(call.Arguments, "original_key")
	}

	if call != nil && call.Name == "create_file_approval" && approvalToken == "" {
		return nil, &ApprovalProposal{Token: "appr-mutating-test"}, ErrApprovalRequired
	}

	return map[string]any{"status": "ok", "executed_tool": call.Name}, nil, nil
}

func TestAgentEngine_RealExecutorBoundaryAdversarialMutation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_real_executor_mutation_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockProv := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProv)

	approvalMgr := NewApprovalManager()
	mutatingExec := &AdversarialMutatingExecutor{}

	engine := NewGeminiAgentEngine(aiRouter, mutatingExec, approvalMgr)

	// Turn 1 requests 2 tool calls:
	// Call 0: custom_tool (executes immediately)
	// Call 1: create_file_approval (requires approval, so task pauses with active PendingTurn!)
	mockProv.SetResponseForPrompt("real executor mutation test", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "",
		FunctionCalls: []router.FunctionCall{
			{
				Name: "custom_tool",
				Arguments: map[string]any{
					"path":         "original_path.txt",
					"original_key": "original_val",
					"nested_map": map[string]any{
						"key": "original_nested_val",
					},
					"nested_slice": []any{"original_slice_item"},
				},
			},
			{
				Name: "create_file_approval",
				Arguments: map[string]any{
					"path":         "approval_path.txt",
					"original_key": "approval_val",
					"nested_map": map[string]any{
						"key": "approval_nested_val",
					},
					"nested_slice": []any{"approval_slice_item"},
				},
			},
		},
	})

	task, err := engine.CreateTask(tempDir, "real executor mutation test", "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// Execute turn through real production executePendingTurn -> ExecuteTool call path
	err = engine.RunTask(context.Background(), task.ID, "")
	if err != nil {
		t.Fatalf("RunTask failed: %v", err)
	}

	mutatingExec.mu.Lock()
	c0Count := mutatingExec.call0Invocations
	c1Count := mutatingExec.call1Invocations
	mutatingExec.mu.Unlock()

	if c0Count != 1 || c1Count != 1 {
		t.Fatalf("Before resume: expected call0Invocations=1, call1Invocations=1, got call0=%d, call1=%d", c0Count, c1Count)
	}

	// Fetch fresh live task state while paused on approval
	engine.mu.RLock()
	liveTask := engine.activeTasks[task.ID]
	engine.mu.RUnlock()

	liveTask.mu.Lock()
	pt := liveTask.PendingTurn
	if pt == nil {
		liveTask.mu.Unlock()
		t.Fatalf("Expected active PendingTurn while paused on approval")
	}

	// 1. Assert PendingTurn.CallStates[0].FunctionCall.Arguments remain UNMUTATED!
	cs0Args := pt.CallStates[0].FunctionCall.Arguments
	if cs0Args["path"] != "original_path.txt" || cs0Args["original_key"] != "original_val" {
		t.Errorf("Mutation leak! PendingTurn.CallStates[0].FunctionCall.Arguments mutated: %v", cs0Args)
	}
	if nestedMap, ok := cs0Args["nested_map"].(map[string]any); !ok || nestedMap["key"] != "original_nested_val" {
		t.Errorf("Mutation leak! PendingTurn.CallStates[0].FunctionCall.Arguments nested_map mutated: %v", nestedMap)
	}

	// 2. Assert PendingTurn.TurnResponse.FunctionCalls[0].Arguments remain UNMUTATED!
	tr0Args := pt.TurnResponse.FunctionCalls[0].Arguments
	if tr0Args["path"] != "original_path.txt" || tr0Args["original_key"] != "original_val" {
		t.Errorf("Mutation leak! PendingTurn.TurnResponse.FunctionCalls[0].Arguments mutated: %v", tr0Args)
	}

	// 3. Assert PendingTurn.CallStates[0].Response contains normal tool result
	if pt.CallStates[0].Response["status"] != "ok" {
		t.Errorf("Tool result missing or invalid in CallStates[0].Response: %v", pt.CallStates[0].Response)
	}
	liveTask.mu.Unlock()

	// Resume task with valid token to complete the turn
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, "appr-mutating-test")
	if err != nil {
		t.Fatalf("ResumeTaskWithApproval failed: %v", err)
	}

	mutatingExec.mu.Lock()
	c0CountAfter := mutatingExec.call0Invocations
	c1CountAfter := mutatingExec.call1Invocations
	mutatingExec.mu.Unlock()

	// Assert Call 0 was NOT re-executed during resume (count remains 1)
	if c0CountAfter != 1 {
		t.Errorf("Call 0 was re-executed during resume! Expected call0Invocations=1, got: %d", c0CountAfter)
	}
	// Assert Call 1 executed exactly once after resume (1 initial approval attempt + 1 approved execution = 2)
	if c1CountAfter != 2 {
		t.Errorf("Call 1 execution count mismatch! Expected call1Invocations=2, got: %d", c1CountAfter)
	}

	// Fetch live task after turn completion
	engine.mu.RLock()
	completedTask := engine.activeTasks[task.ID]
	engine.mu.RUnlock()

	// 4. Assert task.Messages[1] (model turn) FunctionCalls[0].Arguments remain UNMUTATED!
	completedTask.mu.Lock()
	if completedTask.Status != "completed" {
		t.Errorf("Expected final completedTask.Status = 'completed', got: %s", completedTask.Status)
	}
	if len(completedTask.Messages) < 3 {
		completedTask.mu.Unlock()
		t.Fatalf("Expected at least 3 messages in completed task, got: %d", len(completedTask.Messages))
	}

	modelMsg := completedTask.Messages[1]
	origArgs0 := modelMsg.FunctionCalls[0].Arguments
	if origArgs0["path"] != "original_path.txt" || origArgs0["original_key"] != "original_val" {
		t.Errorf("Mutation leak! Model message FunctionCalls[0] arguments mutated: %v", origArgs0)
	}
	if _, found := origArgs0["added_key"]; found {
		t.Errorf("Mutation leak! Model message FunctionCalls[0] received added_key")
	}

	origArgs1 := modelMsg.FunctionCalls[1].Arguments
	if origArgs1["path"] != "approval_path.txt" || origArgs1["original_key"] != "approval_val" {
		t.Errorf("Mutation leak! Model message FunctionCalls[1] arguments mutated: %v", origArgs1)
	}

	// Verify both tool responses recorded correctly in tool response message (Messages[2])
	toolMsg := completedTask.Messages[2]
	if len(toolMsg.FunctionResponses) < 2 {
		t.Fatalf("Expected 2 function responses in tool message, got: %d", len(toolMsg.FunctionResponses))
	}
	if toolMsg.FunctionResponses[0].Response["status"] != "ok" || toolMsg.FunctionResponses[1].Response["status"] != "ok" {
		t.Errorf("Function responses not recorded correctly: %v", toolMsg.FunctionResponses)
	}
	completedTask.mu.Unlock()
}

func TestAgentEngine_ExplicitUnsupportedTypeValidationContract(t *testing.T) {
	dummyChan := make(chan int)

	// Validate supported primitive and container types pass
	supportedMap := map[string]any{
		"str":       "val",
		"int":       42,
		"bool":      true,
		"nil":       nil,
		"sub_map":   map[string]any{"k": "v"},
		"sub_slice": []any{"elem"},
	}

	copied, err := validateAndCopyMap(supportedMap)
	if err != nil {
		t.Errorf("Supported map failed validation: %v", err)
	}
	if copied["str"] != "val" || copied["int"] != 42 {
		t.Errorf("Copied map content mismatch")
	}

	// Validate unsupported non-JSON primitive returns explicit ErrUnsupportedValueType
	unsupportedMap := map[string]any{
		"chan_key": dummyChan,
	}

	_, err = validateAndCopyMap(unsupportedMap)
	if err == nil || !errors.Is(err, ErrUnsupportedValueType) {
		t.Errorf("Expected ErrUnsupportedValueType for unsupported channel type, got: %v", err)
	}
}

func TestAgentEngine_EventListenerIsolation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_event_isolation_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	jail := security.NewWorkspaceJail([]string{tempDir})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := NewWorkspaceService(jail)
	fsSvc := NewFilesystemService(storage)
	psSvc := NewProcessService(procRunner)
	gsSvc := NewGitService(gitClient)
	dsSvc := NewDeviceService(adbClient)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	mockProvider.SetResponseForPrompt("listener test", &router.GenerateResponse{
		Model: "gemini-2.5-flash",
		Text:  "Task completed",
	})

	engine := NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	var listener2Val string

	// Listener 1 mutates event data
	engine.SubscribeEvents("listener-1", func(evt TaskEvent) {
		if evt.Data != nil {
			evt.Data["workspace_id"] = "MUTATED_BY_LISTENER_1"
		}
	})

	// Listener 2 observes event data
	engine.SubscribeEvents("listener-2", func(evt TaskEvent) {
		if evt.Data != nil && evt.Type == EventTaskStarted {
			if val, ok := evt.Data["workspace_id"].(string); ok {
				listener2Val = val
			}
		}
	})

	task, _ := engine.CreateTask(tempDir, "listener test", "gemini-2.5-flash")
	_ = engine.RunTask(context.Background(), task.ID, "")

	// Verify Listener 2 received original unmutated workspace_id
	if listener2Val == "MUTATED_BY_LISTENER_1" {
		t.Errorf("Mutation leak! Listener 1 mutated event data observed by Listener 2")
	}

	// Verify stored task events remain unmutated
	snap, _ := engine.GetTask(task.ID)
	if len(snap.Events) > 0 && snap.Events[0].Data != nil {
		if snap.Events[0].Data["workspace_id"] == "MUTATED_BY_LISTENER_1" {
			t.Errorf("Mutation leak! Listener 1 mutated stored task event data")
		}
	}
}

func TestAgentEngine_RealEventRejection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_event_rejection_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	approvalMgr := NewApprovalManager()
	engine := NewGeminiAgentEngine(aiRouter, nil, approvalMgr)

	task, _ := engine.CreateTask(tempDir, "rejection test", "gemini-2.5-flash")

	receivedEvents := 0
	engine.SubscribeEvents("listener-test", func(evt TaskEvent) {
		receivedEvents++
	})

	dummyChan := make(chan int)

	// 1. Attempt emitting invalid event with unsupported channel type -> MUST return ErrUnsupportedValueType
	err = engine.emitEvent(task, EventTaskStarted, "invalid event", map[string]any{
		"bad_key": dummyChan,
	})

	if err == nil || !errors.Is(err, ErrUnsupportedValueType) {
		t.Fatalf("Expected ErrUnsupportedValueType for event payload with channel, got: %v", err)
	}

	// Verify invalid event was NOT stored in task.Events
	snap0, _ := engine.GetTask(task.ID)
	if len(snap0.Events) != 0 {
		t.Errorf("Invalid event was incorrectly appended to task.Events: %v", snap0.Events)
	}

	// Verify listener was NOT invoked for invalid event
	if receivedEvents != 0 {
		t.Errorf("Listener was incorrectly invoked for invalid event")
	}

	// 2. Emit valid event with nested map
	inputData := map[string]any{
		"workspace_id": "ws-valid",
		"nested": map[string]any{
			"key": "orig_val",
		},
	}

	err = engine.emitEvent(task, EventTaskStarted, "valid event", inputData)
	if err != nil {
		t.Fatalf("Failed to emit valid event: %v", err)
	}

	// Caller mutates input map AFTER emission
	inputData["workspace_id"] = "MUTATED_BY_CALLER"
	inputData["nested"].(map[string]any)["key"] = "MUTATED_BY_CALLER"

	// Verify stored task event remained UNMUTATED by caller
	snap1, _ := engine.GetTask(task.ID)
	if len(snap1.Events) != 1 {
		t.Fatalf("Expected 1 stored event, got: %d", len(snap1.Events))
	}

	storedData := snap1.Events[0].Data
	if storedData["workspace_id"] == "MUTATED_BY_CALLER" || storedData["nested"].(map[string]any)["key"] == "MUTATED_BY_CALLER" {
		t.Errorf("Caller map mutation leaked into stored task event: %v", storedData)
	}
}

func TestAgentEngine_RequiredEventFailurePolicy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_required_event_policy_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockProvider := router.NewMockGeminiProvider()
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", mockProvider)

	approvalMgr := NewApprovalManager()
	engine := NewGeminiAgentEngine(aiRouter, nil, approvalMgr)

	task, _ := engine.CreateTask(tempDir, "policy test", "gemini-2.5-flash")

	dummyChan := make(chan int)
	badData := map[string]any{
		"proposal": dummyChan,
	}

	// Class B Required Event Emission Failure -> MUST return ErrUnsupportedValueType and NOT store event
	err = engine.emitEvent(task, EventApprovalRequired, "requires approval", badData)
	if err == nil || !errors.Is(err, ErrUnsupportedValueType) {
		t.Errorf("Expected EventApprovalRequired with channel payload to fail with ErrUnsupportedValueType, got: %v", err)
	}

	// Verify task.Events did NOT record the failing Class B event
	snap, _ := engine.GetTask(task.ID)
	if len(snap.Events) != 0 {
		t.Errorf("Expected 0 events recorded for failed Class B event emission, got: %d", len(snap.Events))
	}
}

type FailingFailingAIProvider struct {
	router.Provider
}

func (p *FailingFailingAIProvider) Name() string { return "Gemini API" }
func (p *FailingFailingAIProvider) DefaultModel() string { return "gemini-2.5-flash" }
func (p *FailingFailingAIProvider) Generate(ctx context.Context, req router.GenerateRequest) (*router.GenerateResponse, error) {
	return nil, errors.New("simulated AI generation failure")
}

func TestAgentEngine_ProductionPathTaskFailedAndCancelledEventFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_failed_cancelled_policy_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempDir)

	failingProv := &FailingFailingAIProvider{}
	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", failingProv)

	approvalMgr := NewApprovalManager()
	engine := NewGeminiAgentEngine(aiRouter, nil, approvalMgr)

	// A. Test Task Failure with Event Emission Failure
	failedTaskCount := 0
	engine.SetEventHook(func(evtType EventType, data map[string]any) error {
		if evtType == EventTaskFailed {
			failedTaskCount++
			return ErrUnsupportedValueType
		}
		return nil
	})

	taskF, _ := engine.CreateTask(tempDir, "failing prompt", "gemini-2.5-flash")
	err = engine.RunTask(context.Background(), taskF.ID, "")

	if err == nil {
		t.Fatalf("Expected RunTask to fail when AI generation fails")
	}

	// Verify error structurally wraps both original AI generation error AND event emission error
	if !errors.Is(err, ErrUnsupportedValueType) || !strings.Contains(err.Error(), "simulated AI generation failure") {
		t.Errorf("Expected errors.Is(err, ErrUnsupportedValueType) and original error, got: %v", err)
	}

	// Verify task remains in 'failed' status and failedTaskCount is exactly 1 (no recursive event loop)
	snapF, _ := engine.GetTask(taskF.ID)
	if snapF.Status != "failed" || !strings.Contains(snapF.ErrorText, "simulated AI generation failure") {
		t.Errorf("Expected task status 'failed' with original errorText, got status=%s, errorText=%s", snapF.Status, snapF.ErrorText)
	}
	if failedTaskCount != 1 {
		t.Errorf("Expected EventTaskFailed hook invoked exactly once (no recursive loop), got: %d", failedTaskCount)
	}

	// B. Test Task Cancellation with Event Emission Failure
	cancelTaskCount := 0

	blockingProv := &BlockingAIProvider{blockChan: make(chan struct{})}
	aiRouterC := router.NewAIRouter()
	aiRouterC.RegisterProvider("Gemini API", blockingProv)

	engineC := NewGeminiAgentEngine(aiRouterC, nil, approvalMgr)
	engineC.SetEventHook(func(evtType EventType, data map[string]any) error {
		if evtType == EventTaskCancelled {
			cancelTaskCount++
			return ErrUnsupportedValueType
		}
		return nil
	})

	taskC, _ := engineC.CreateTask(tempDir, "cancelling prompt", "gemini-2.5-flash")

	ctxC, cancelC := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelC()

	errC := engineC.RunTask(ctxC, taskC.ID, "")
	if errC == nil {
		t.Fatalf("Expected RunTask to fail when context is cancelled")
	}

	if !errors.Is(errC, ErrUnsupportedValueType) {
		t.Errorf("Expected errors.Is(errC, ErrUnsupportedValueType) for cancellation event failure, got: %v", errC)
	}

	// Verify task status is 'cancelled'
	snapC, _ := engineC.GetTask(taskC.ID)
	if snapC.Status != "cancelled" {
		t.Errorf("Expected task status 'cancelled', got: %s", snapC.Status)
	}
	if cancelTaskCount < 1 {
		t.Errorf("Expected EventTaskCancelled hook invoked, got: %d", cancelTaskCount)
	}

	// C. Test Explicit CancelTaskWithError with Event Emission Failure
	engineC2 := NewGeminiAgentEngine(aiRouterC, nil, approvalMgr)
	engineC2.SetEventHook(func(evtType EventType, data map[string]any) error {
		if evtType == EventTaskCancelled {
			return ErrUnsupportedValueType
		}
		return nil
	})

	taskC2, _ := engineC2.CreateTask(tempDir, "explicit cancellation prompt", "gemini-2.5-flash")
	foundC2, errC2 := engineC2.CancelTaskWithError(taskC2.ID)
	if !foundC2 {
		t.Fatalf("Expected CancelTaskWithError to find task")
	}
	if errC2 == nil || !errors.Is(errC2, ErrUnsupportedValueType) {
		t.Errorf("Expected CancelTaskWithError to return error wrapping ErrUnsupportedValueType, got: %v", errC2)
	}

	snapC2, _ := engineC2.GetTask(taskC2.ID)
	if snapC2.Status != "cancelled" {
		t.Errorf("Expected task status 'cancelled', got: %s", snapC2.Status)
	}

	// D. Test Cancellation Deduplication (Explicit Cancellation vs Context Cancellation)
	dupEventCount := 0
	engineD := NewGeminiAgentEngine(aiRouterC, nil, approvalMgr)
	engineD.SetEventHook(func(evtType EventType, data map[string]any) error {
		if evtType == EventTaskCancelled {
			dupEventCount++
		}
		return nil
	})

	taskD, _ := engineD.CreateTask(tempDir, "dedup prompt", "gemini-2.5-flash")
	_ = engineD.CancelTask(taskD.ID)
	_ = engineD.emitCancellationEvent(taskD, "second cancellation")

	if dupEventCount != 1 {
		t.Errorf("Expected exactly 1 cancellation event attempt per cancellation transition, got: %d", dupEventCount)
	}
}
