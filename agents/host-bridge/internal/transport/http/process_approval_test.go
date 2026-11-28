package http

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

// newApprovalServer builds a Server with a real approval manager and a real command policy, so these
// tests exercise the production approval boundary rather than a stub.
func newApprovalServer(t *testing.T) (*Server, string) {
	t.Helper()

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	srv := newContractServer(t, "contract-host-secret-32-bytes-ok")
	srv.SetApprovalManager(application.NewApprovalManager())
	return srv, workspace
}

// issueApproval creates a proposal exactly as HandleCreateProcessApproval does.
func issueApproval(t *testing.T, srv *Server, sessionID, deviceID, workspace, command string, args []string) *application.ApprovalProposal {
	t.Helper()
	proposal, err := srv.approvalMgr.CreateProposal(
		approvalOwnerID(sessionID, deviceID),
		workspace,
		"execute_command",
		"", "", "", "",
		command, args,
		"gradle executes project-defined build logic",
		processApprovalTTL,
	)
	if err != nil {
		t.Fatalf("failed to create approval proposal: %v", err)
	}
	return proposal
}

// TestProcessApproval_MissingTokenFailsClosed proves a Gradle execution without an approval token is
// rejected at the authorization boundary.
func TestProcessApproval_MissingTokenFailsClosed(t *testing.T) {
	srv, workspace := newApprovalServer(t)

	spec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build"}}
	err := srv.consumeProcessApproval("sess-1", "dev-1", spec)
	if !errors.Is(err, application.ErrApprovalRequirementMissing) {
		t.Fatalf("expected ErrApprovalRequirementMissing, got %v", err)
	}
	if reason := approvalRejectionReason(err); reason != "approval_missing" {
		t.Fatalf("expected approval_missing reason, got %q", reason)
	}
}

// TestProcessApproval_NoManagerFailsClosed proves a server without an approval manager denies rather
// than allowing the command.
func TestProcessApproval_NoManagerFailsClosed(t *testing.T) {
	srv, workspace := newApprovalServer(t)
	srv.SetApprovalManager(nil)

	spec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build"}, ApprovalToken: "appr-anything"}
	if err := srv.consumeProcessApproval("sess-1", "dev-1", spec); err == nil {
		t.Fatalf("expected rejection when no approval manager is configured")
	}
}

// TestProcessApproval_ConsumesValidToken proves the documented two-step flow authorizes execution once.
func TestProcessApproval_ConsumesValidToken(t *testing.T) {
	srv, workspace := newApprovalServer(t)
	proposal := issueApproval(t, srv, "sess-1", "dev-1", workspace, "gradlew", []string{"build"})

	spec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build"}, ApprovalToken: proposal.Token}
	if err := srv.consumeProcessApproval("sess-1", "dev-1", spec); err != nil {
		t.Fatalf("expected valid approval to be consumed, got %v", err)
	}
}

// TestProcessApproval_ReplayIsRejected proves single-use semantics.
func TestProcessApproval_ReplayIsRejected(t *testing.T) {
	srv, workspace := newApprovalServer(t)
	proposal := issueApproval(t, srv, "sess-1", "dev-1", workspace, "gradlew", []string{"build"})

	spec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build"}, ApprovalToken: proposal.Token}
	if err := srv.consumeProcessApproval("sess-1", "dev-1", spec); err != nil {
		t.Fatalf("first consumption should succeed, got %v", err)
	}
	if err := srv.consumeProcessApproval("sess-1", "dev-1", spec); err == nil {
		t.Fatalf("expected replay of a consumed token to be rejected")
	}
}

// TestProcessApproval_ArgumentAndScopeBinding proves a token cannot be widened to other arguments,
// another workspace, or another session.
func TestProcessApproval_ArgumentAndScopeBinding(t *testing.T) {
	srv, workspace := newApprovalServer(t)
	proposal := issueApproval(t, srv, "sess-1", "dev-1", workspace, "gradlew", []string{"build"})

	cases := []struct {
		name      string
		sessionID string
		spec      *domain.ProcessSpec
	}{
		{
			name:      "different arguments",
			sessionID: "sess-1",
			spec:      &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build", "-x", "test"}, ApprovalToken: proposal.Token},
		},
		{
			name:      "different command",
			sessionID: "sess-1",
			spec:      &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradle", Args: []string{"build"}, ApprovalToken: proposal.Token},
		},
		{
			name:      "different workspace",
			sessionID: "sess-1",
			spec:      &domain.ProcessSpec{WorkspaceID: "other-workspace", Command: "gradlew", Args: []string{"build"}, ApprovalToken: proposal.Token},
		},
		{
			name:      "different session",
			sessionID: "sess-2",
			spec:      &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build"}, ApprovalToken: proposal.Token},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := srv.consumeProcessApproval(tc.sessionID, "dev-1", tc.spec)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
			if !errors.Is(err, application.ErrApprovalMismatch) {
				t.Fatalf("expected ErrApprovalMismatch for %s, got %v", tc.name, err)
			}
		})
	}
}

// TestProcessApproval_ExpiredTokenIsRejected proves TTL enforcement.
func TestProcessApproval_ExpiredTokenIsRejected(t *testing.T) {
	srv, workspace := newApprovalServer(t)

	proposal, err := srv.approvalMgr.CreateProposal(
		approvalOwnerID("sess-1", "dev-1"), workspace, "execute_command", "", "", "", "",
		"gradlew", []string{"build"}, "expired", -time.Minute,
	)
	if err != nil {
		t.Fatalf("failed to create proposal: %v", err)
	}

	spec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"build"}, ApprovalToken: proposal.Token}
	consumeErr := srv.consumeProcessApproval("sess-1", "dev-1", spec)
	if !errors.Is(consumeErr, application.ErrApprovalExpired) {
		t.Fatalf("expected ErrApprovalExpired, got %v", consumeErr)
	}
	if reason := approvalRejectionReason(consumeErr); reason != "token_expired" {
		t.Fatalf("expected token_expired reason, got %q", reason)
	}
}

// TestProcessApproval_OwnerBindingIsDeterministic proves the session/device binding used to scope
// tokens cannot collide across sessions or devices.
func TestProcessApproval_OwnerBindingIsDeterministic(t *testing.T) {
	a := approvalOwnerID("sess-1", "dev-1")
	b := approvalOwnerID("sess-1", "dev-1")
	c := approvalOwnerID("sess-2", "dev-1")
	d := approvalOwnerID("sess-1", "dev-2")

	if a != b {
		t.Fatalf("expected stable owner id for identical inputs")
	}
	if a == c || a == d {
		t.Fatalf("expected distinct owner ids for different session or device")
	}
}

// TestProcessApproval_WipesTokenFromSpec proves the token cannot leak into the child process or a
// response body after the decision is made.
func TestProcessApproval_WipesTokenFromSpec(t *testing.T) {
	spec := &domain.ProcessSpec{Command: "gradlew", ApprovalToken: "appr-secret-token"}
	wipeApprovalToken(spec)
	if spec.ApprovalToken != "" {
		t.Fatalf("expected approval token to be cleared, got %q", spec.ApprovalToken)
	}
}

// TestRequiresApproval_DelegatesToPolicy proves both execution boundaries share one classification.
func TestRequiresApproval_DelegatesToPolicy(t *testing.T) {
	srv, workspace := newApprovalServer(t)

	gradleSpec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "gradlew", Args: []string{"assembleDebug"}}
	if required, reason := srv.processSvc.RequiresApproval(gradleSpec); !required || reason == "" {
		t.Fatalf("expected gradle to require approval, got (%v, %q)", required, reason)
	}

	gitSpec := &domain.ProcessSpec{WorkspaceID: workspace, Command: "git", Args: []string{"status"}}
	if required, _ := srv.processSvc.RequiresApproval(gitSpec); required {
		t.Fatalf("expected git status not to require approval")
	}

	// The policy rejects unsafe Gradle arguments before any approval can be issued for them.
	_, err := srv.processSvc.ExecuteProcess(t.Context(), &domain.ProcessSpec{
		WorkspaceID: workspace, Command: "gradlew", Args: []string{"-Ievil.gradle", "build"},
	})
	if err == nil {
		t.Fatalf("expected policy to reject the init-script override")
	} else if !errors.Is(err, domain.ErrCommandNotAllowed) {
		t.Fatalf("expected ErrCommandNotAllowed, got %v", err)
	}
}

// TestApprovalServer_HasFullSessionPermissions guards the test harness itself: a session created for
// these tests must carry process:execute, otherwise the assertions above would be vacuous.
func TestApprovalServer_HasFullSessionPermissions(t *testing.T) {
	srv, _ := newApprovalServer(t)
	perms := security.NewFullSessionPermissions()
	if !perms.IsAuthorized(security.PermissionProcessExecute) {
		t.Fatalf("expected full permissions to include process:execute")
	}
	if srv.processSvc == nil {
		t.Fatalf("expected process service to be wired")
	}
}
