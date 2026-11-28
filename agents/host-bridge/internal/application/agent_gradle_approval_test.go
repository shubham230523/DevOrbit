package application

import (
	"context"
	"errors"
	"testing"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
)

// TestAgentExecuteCommand_GradleCannotExecuteWithoutHumanApproval proves the AI path cannot authorize
// its own Gradle execution: the tool returns a proposal and never a process result.
func TestAgentExecuteCommand_GradleCannotExecuteWithoutHumanApproval(t *testing.T) {
	jail := security.NewWorkspaceJail([]string{t.TempDir()})
	policy := security.NewDefaultCommandPolicy()
	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	runner := process.NewProcessRunner(policy, jail)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(
		NewWorkspaceService(jail),
		NewFilesystemService(storage),
		NewProcessService(runner),
		NewGitService(git.NewGitClient(jail)),
		NewDeviceService(adb.NewADBClient("adb")),
		approvalMgr,
	)

	workspaceID := "ws-agent-gradle"
	call := &router.FunctionCall{
		Name: router.ToolExecuteCommand,
		Arguments: map[string]any{
			"command": "./gradlew",
			"args":    []any{"assembleRelease"},
		},
	}

	result, proposal, err := executor.ExecuteTool(context.Background(), "task-gradle-1", workspaceID, call, "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired for an AI-initiated Gradle command, got %v", err)
	}
	if result != nil {
		t.Fatalf("no process result may be returned before approval, got %v", result)
	}
	if proposal == nil {
		t.Fatalf("expected an approval proposal to be created")
	}
	if proposal.Command != "./gradlew" {
		t.Fatalf("proposal must bind the exact command, got %q", proposal.Command)
	}

	// Presenting the proposal's own token back to the *same* call path is the only way the agent can
	// proceed; the manager still requires an exact match, so a fabricated token fails.
	if _, _, err := executor.ExecuteTool(context.Background(), "task-gradle-1", workspaceID, call, "appr-forged-token"); err == nil {
		t.Fatalf("expected a forged approval token to be rejected")
	}

	// A genuine approval authorizes exactly this command.
	if _, err := approvalMgr.ConsumeApproval(proposal.Token, "task-gradle-1", workspaceID, router.ToolExecuteCommand, "", "", "./gradlew", []string{"assembleRelease"}, "", ""); err != nil {
		t.Fatalf("expected the issued token to be consumable once, got %v", err)
	}
}
