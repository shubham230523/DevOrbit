package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
)

// newGradleAgentEnv wires the agent tool executor exactly as production does: the shared ProcessService
// gate plus the authoritative process approval store.
func newGradleAgentEnv(t *testing.T) (*AgentToolExecutor, *ProcessApprovalStore, context.Context, string) {
	t.Helper()

	workspace := t.TempDir()
	jail := security.NewWorkspaceJail([]string{workspace})
	policy := security.NewDefaultCommandPolicy()
	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	runner := process.NewProcessRunner(policy, jail)

	procSvc := NewProcessService(runner)
	store := NewProcessApprovalStore(10 * time.Minute)
	procSvc.SetProcessApprovalStore(store)

	approvalMgr := NewApprovalManager()
	executor := NewAgentToolExecutor(
		NewWorkspaceService(jail),
		NewFilesystemService(storage),
		procSvc,
		NewGitService(git.NewGitClient(jail)),
		NewDeviceService(adb.NewADBClient("adb")),
		approvalMgr,
	)
	executor.SetProcessApprovalStore(store)

	ctx := WithTaskOwner(context.Background(), "sess-agent", "dev-agent")
	return executor, store, ctx, workspace
}

func gradleCall() *router.FunctionCall {
	return &router.FunctionCall{
		Name: router.ToolExecuteCommand,
		Arguments: map[string]any{
			"command": "./gradlew",
			"args":    []any{"assembleRelease"},
		},
	}
}

// TestAgentGradle_ProposalGrantsNoExecution proves the AI path cannot execute a Gradle command by
// requesting it: the tool returns a proposal and no process result.
func TestAgentGradle_ProposalGrantsNoExecution(t *testing.T) {
	executor, _, ctx, _ := newGradleAgentEnv(t)

	result, proposal, err := executor.ExecuteTool(ctx, "task-gradle-1", "ws-gradle", gradleCall(), "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	if result != nil {
		t.Fatalf("no process result may be returned before approval, got %v", result)
	}
	if proposal == nil || proposal.Token == "" {
		t.Fatalf("expected an approval proposal")
	}
	// The agent receives the approval ID and the digest, never an execution token.
	if !isProcessApprovalID(proposal.Token) {
		t.Fatalf("expected the proposal token to be a process approval id, got %q", proposal.Token)
	}
	if proposal.ExpectedSHA256 == "" {
		t.Fatalf("expected the proposal to carry the command digest")
	}
}

// TestAgentGradle_PendingProposalCannotExecute proves the approval id alone is not an authorization.
func TestAgentGradle_PendingProposalCannotExecute(t *testing.T) {
	executor, _, ctx, _ := newGradleAgentEnv(t)

	_, proposal, err := executor.ExecuteTool(ctx, "task-gradle-2", "ws-gradle", gradleCall(), "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}

	// Using the approval id without a recorded human decision must fail closed.
	if _, _, err := executor.ExecuteTool(ctx, "task-gradle-2", "ws-gradle", gradleCall(), proposal.Token); !errors.Is(err, ErrProcessApprovalNotApproved) {
		t.Fatalf("expected ErrProcessApprovalNotApproved, got %v", err)
	}
}

// TestAgentGradle_HumanDecisionEnablesExecution proves the decision is what authorizes execution, and
// that the authorization is single use.
func TestAgentGradle_HumanDecisionEnablesExecution(t *testing.T) {
	executor, store, ctx, _ := newGradleAgentEnv(t)

	_, proposal, err := executor.ExecuteTool(ctx, "task-gradle-3", "ws-gradle", gradleCall(), "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}

	// Without the human decision nothing can execute.
	if _, _, err := executor.ExecuteTool(ctx, "task-gradle-3", "ws-gradle", gradleCall(), proposal.Token); err == nil {
		t.Fatalf("expected rejection before the decision")
	}

	// The human decision (recorded against the exact proposal digest) enables execution.
	if _, err := store.Decide(proposal.Token, "sess-agent", "dev-agent", "approve", proposal.ExpectedSHA256); err != nil {
		t.Fatalf("Decide(approve) failed: %v", err)
	}

	// Execution now proceeds to the gate. The workspace has no wrapper, so the process cannot launch;
	// what matters is that the failure is a command/launch error, not an approval error.
	_, _, err = executor.ExecuteTool(ctx, "task-gradle-3", "ws-gradle", gradleCall(), proposal.Token)
	if err != nil && isApprovalFailure(err) {
		t.Fatalf("expected the approval gate to be satisfied, got approval error: %v", err)
	}

	// The approval is consumed: a replay fails closed.
	if _, _, err := executor.ExecuteTool(ctx, "task-gradle-3", "ws-gradle", gradleCall(), proposal.Token); !isApprovalFailure(err) {
		t.Fatalf("expected replay to be rejected by the approval boundary, got %v", err)
	}
}

// TestAgentGradle_RejectionPreventsExecution proves a rejected proposal never authorizes anything.
func TestAgentGradle_RejectionPreventsExecution(t *testing.T) {
	executor, store, ctx, _ := newGradleAgentEnv(t)

	_, proposal, err := executor.ExecuteTool(ctx, "task-gradle-4", "ws-gradle", gradleCall(), "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	if _, err := store.Decide(proposal.Token, "sess-agent", "dev-agent", "reject", ""); err != nil {
		t.Fatalf("Decide(reject) failed: %v", err)
	}

	if _, _, err := executor.ExecuteTool(ctx, "task-gradle-4", "ws-gradle", gradleCall(), proposal.Token); !isApprovalFailure(err) {
		t.Fatalf("expected a rejected proposal to fail closed, got %v", err)
	}
}

// TestAgentGradle_LegacyApprovalTokenCannotAuthorizeExecution is the regression test for the MP26.2
// gap: a token minted by the task-oriented ApprovalManager must not authorize Gradle execution.
func TestAgentGradle_LegacyApprovalTokenCannotAuthorizeExecution(t *testing.T) {
	workspace := t.TempDir()
	jail := security.NewWorkspaceJail([]string{workspace})
	policy := security.NewDefaultCommandPolicy()
	runner := process.NewProcessRunner(policy, jail)
	procSvc := NewProcessService(runner)
	store := NewProcessApprovalStore(10 * time.Minute)
	procSvc.SetProcessApprovalStore(store)

	legacy := NewApprovalManager()
	executor := NewAgentToolExecutor(
		NewWorkspaceService(jail),
		NewFilesystemService(filesystem.NewStorageEngine(jail, 1024*1024)),
		procSvc,
		NewGitService(git.NewGitClient(jail)),
		NewDeviceService(adb.NewADBClient("adb")),
		legacy,
	)
	executor.SetProcessApprovalStore(store)

	// A perfectly valid legacy proposal+token for the same command.
	legacyProposal, err := legacy.CreateProposal("task-legacy", "ws-gradle", router.ToolExecuteCommand,
		"", "", "", "", "./gradlew", []string{"assembleRelease"}, "legacy", 10*time.Minute)
	if err != nil {
		t.Fatalf("failed to create legacy proposal: %v", err)
	}

	ctx := WithTaskOwner(context.Background(), "sess-agent", "dev-agent")
	if _, _, err := executor.ExecuteTool(ctx, "task-legacy", "ws-gradle", gradleCall(), legacyProposal.Token); !isApprovalFailure(err) {
		t.Fatalf("expected a legacy approval token to be rejected for Gradle execution, got %v", err)
	}
}

// TestAgentGradle_MissingProcessApprovalStoreFailsClosed proves the agent route cannot fall back to
// the legacy manager when the authoritative service is absent.
func TestAgentGradle_MissingProcessApprovalStoreFailsClosed(t *testing.T) {
	executor, _, ctx, _ := newGradleAgentEnv(t)
	executor.SetProcessApprovalStore(nil)

	if _, _, err := executor.ExecuteTool(ctx, "task-gradle-5", "ws-gradle", gradleCall(), ""); !errors.Is(err, ErrApprovalRequirementMissing) {
		t.Fatalf("expected ErrApprovalRequirementMissing without a store, got %v", err)
	}
}

// TestAgentGradle_OwnerIsRequired proves approval cannot be created for an unbound principal.
func TestAgentGradle_OwnerIsRequired(t *testing.T) {
	executor, _, _, _ := newGradleAgentEnv(t)

	if _, _, err := executor.ExecuteTool(context.Background(), "task-gradle-6", "ws-gradle", gradleCall(), ""); !errors.Is(err, ErrProcessApprovalOwner) {
		t.Fatalf("expected ErrProcessApprovalOwner without a task owner, got %v", err)
	}
}

// TestAgentNonGradle_KeepsTaskApproval proves git and other non-code-executing commands still use the
// existing task approval flow and are not routed through the process approval store.
func TestAgentNonGradle_KeepsTaskApproval(t *testing.T) {
	executor, _, ctx, _ := newGradleAgentEnv(t)

	call := &router.FunctionCall{
		Name:      router.ToolExecuteCommand,
		Arguments: map[string]any{"command": "git", "args": []any{"status"}},
	}

	_, proposal, err := executor.ExecuteTool(ctx, "task-git-1", "ws-gradle", call, "")
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected task approval to be required for git, got %v", err)
	}
	if proposal == nil {
		t.Fatalf("expected a task approval proposal")
	}
	if isProcessApprovalID(proposal.Token) {
		t.Fatalf("git must use the task approval flow, not the process approval store")
	}
}

// isProcessApprovalID reports whether a token is a process-approval identifier.
func isProcessApprovalID(token string) bool {
	return len(token) > 6 && token[:6] == "pappr-"
}

// isApprovalFailure reports whether an error came from the approval boundary.
func isApprovalFailure(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrApprovalRequirementMissing) ||
		errors.Is(err, ErrProcessApprovalNotFound) ||
		errors.Is(err, ErrProcessApprovalNotPending) ||
		errors.Is(err, ErrProcessApprovalExpired) ||
		errors.Is(err, ErrProcessApprovalOwner) ||
		errors.Is(err, ErrProcessApprovalDigest) ||
		errors.Is(err, ErrProcessApprovalScope) ||
		errors.Is(err, ErrProcessApprovalToken) ||
		errors.Is(err, ErrApprovalTokenNotFound) ||
		errors.Is(err, ErrApprovalMismatch)
}
