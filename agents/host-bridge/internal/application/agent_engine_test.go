package application

import (
	"context"
	"os"
	"path/filepath"
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

	// 2. Test concurrent resume rejection
	taskState.mu.Lock()
	taskState.Status = "running"
	taskState.mu.Unlock()
	err = engine.ResumeTaskWithApproval(context.Background(), task.ID, taskState.ApprovalToken)
	if err == nil || err.Error() != "task is already running" {
		t.Errorf("Expected 'task is already running' error for concurrent resume attempt, got: %v", err)
	}

	taskState.mu.Lock()
	taskState.Status = "approval_required"
	taskState.mu.Unlock()

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
