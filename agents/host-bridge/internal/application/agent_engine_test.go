package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
