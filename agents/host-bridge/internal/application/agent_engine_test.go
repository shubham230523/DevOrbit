package application

import (
	"context"
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

func TestAgentEngine_DeterministicBlockingConcurrentOwnership(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_blocking_ownership_*")
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

	enteredChan := make(chan struct{}, 1)
	blockChan := make(chan struct{})
	blockingProv := &BlockingAIProvider{enteredChan: enteredChan, blockChan: blockChan}

	aiRouter := router.NewAIRouter()
	aiRouter.RegisterProvider("Gemini API", blockingProv)

	engine := NewGeminiAgentEngine(aiRouter, executor, approvalMgr)

	task, err := engine.CreateTask(tempDir, "blocking task test", "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// 1. Goroutine 0 starts RunTask and enters AI generation
	go0Done := make(chan error, 1)
	go func() {
		go0Done <- engine.RunTask(context.Background(), task.ID, "")
	}()

	// Wait deterministically for Goroutine 0 to acquire execution ownership and enter AI generation
	<-enteredChan

	// 2. Goroutines 1..5 call RunTask simultaneously -> MUST ALL receive "task is already running"
	var wg sync.WaitGroup
	var mu sync.Mutex
	alreadyRunningErrs := 0

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rErr := engine.RunTask(context.Background(), task.ID, "")
			if rErr != nil && rErr.Error() == "task is already running" {
				mu.Lock()
				alreadyRunningErrs++
				mu.Unlock()
			}
		}()
	}

	// 3. Goroutines 6..10 call ResumeTaskWithApproval simultaneously -> MUST ALL receive "task is already running"
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resErr := engine.ResumeTaskWithApproval(context.Background(), task.ID, "appr-dummy")
			if resErr != nil && resErr.Error() == "task is already running" {
				mu.Lock()
				alreadyRunningErrs++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if alreadyRunningErrs != 10 {
		t.Errorf("Expected 10 'task is already running' errors while task was executing, got: %d", alreadyRunningErrs)
	}

	// Unblock Goroutine 0
	close(blockChan)
	go0Err := <-go0Done
	if go0Err != nil {
		t.Fatalf("Goroutine 0 RunTask failed: %v", go0Err)
	}

	taskFinal, _ := engine.GetTask(task.ID)
	if taskFinal.Status != "completed" {
		t.Errorf("Expected task status 'completed', got: %s", taskFinal.Status)
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
