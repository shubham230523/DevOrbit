package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
	"devorbit.net/services/connection-broker/pkg/observability"
)

// processApprovalTTL bounds how long a Gradle execution authorization stays usable.
const processApprovalTTL = 10 * time.Minute

// approvalOwnerID binds an approval token to the session and device that requested it. The value is
// stored in the proposal's TaskID field so the existing, already-hardened approval manager enforces
// the binding; no new approval store is introduced.
func approvalOwnerID(sessionID, deviceID string) string {
	return "process-approval:" + sessionID + ":" + deviceID
}

// wipeApprovalToken clears the token from the spec so it cannot be logged, echoed, or inherited by
// the child process environment.
func wipeApprovalToken(spec *domain.ProcessSpec) {
	spec.ApprovalToken = ""
}

// consumeProcessApproval validates and consumes the single-use approval token carried by the spec.
// It fails closed when no approval manager is configured.
func (s *Server) consumeProcessApproval(sessionID, deviceID string, spec *domain.ProcessSpec) error {
	if s.approvalMgr == nil {
		return errors.New("approval manager not configured")
	}
	if strings.TrimSpace(spec.ApprovalToken) == "" {
		return application.ErrApprovalRequirementMissing
	}

	_, err := s.approvalMgr.ConsumeApproval(
		spec.ApprovalToken,
		approvalOwnerID(sessionID, deviceID),
		spec.WorkspaceID,
		"execute_command",
		"", "", // no target path / hash for command approvals
		spec.Command,
		spec.Args,
		"", "", // no content fields for command approvals
	)
	if err != nil {
		s.securityEvent(observability.LevelWarn, EventProcessApprovalRejected, map[string]any{
			"command":   spec.Command,
			"arg_count": len(spec.Args),
			"workspace": spec.WorkspaceID,
			"reason":    approvalRejectionReason(err),
		})
		s.incrementMetric("devorbit_process_approvals_rejected_total")
		return err
	}
	return nil
}

// approvalRejectionReason maps approval errors to stable, non-sensitive reason codes.
func approvalRejectionReason(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, application.ErrApprovalTokenNotFound):
		return "token_not_found_or_consumed"
	case errors.Is(err, application.ErrApprovalExpired):
		return "token_expired"
	case errors.Is(err, application.ErrApprovalMismatch):
		return "token_scope_mismatch"
	case errors.Is(err, application.ErrApprovalRequirementMissing):
		return "approval_missing"
	default:
		return "approval_error"
	}
}

// HandleCreateProcessApproval issues a single-use authorization for a command that requires an
// explicit user decision (currently the Gradle family).
//
// The proposal is bound to the requesting session+device, the workspace, the exact command and the
// exact argument vector, expires after processApprovalTTL, and can be consumed once. Because this
// endpoint requires an authenticated session with process:execute, an AI agent (which has no session)
// cannot mint an approval to authorize its own command.
func (s *Server) HandleCreateProcessApproval(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionProcessExecute)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.approvalMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "approval manager not configured")
		return
	}

	var req struct {
		WorkspaceID string   `json:"workspace_id"`
		Command     string   `json:"command"`
		Args        []string `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid approval request JSON")
		return
	}
	if strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.Command) == "" {
		writeError(w, http.StatusBadRequest, "workspace_id and command are required")
		return
	}

	spec := &domain.ProcessSpec{WorkspaceID: req.WorkspaceID, Command: req.Command, Args: req.Args}
	required, reason := s.processSvc.RequiresApproval(spec)
	if !required {
		writeError(w, http.StatusBadRequest, "command does not require approval; submit it directly")
		return
	}

	proposal, err := s.approvalMgr.CreateProposal(
		approvalOwnerID(authCtx.SessionID, authCtx.DeviceID),
		req.WorkspaceID,
		"execute_command",
		"", "", "", "",
		req.Command,
		req.Args,
		reason,
		processApprovalTTL,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create approval proposal")
		return
	}

	// Audit the authorization request; the token itself must never be logged.
	s.securityEvent(observability.LevelInfo, EventProcessApprovalIssued, map[string]any{
		"command":    req.Command,
		"arg_count":  len(req.Args),
		"workspace":  req.WorkspaceID,
		"expires_in": processApprovalTTL.String(),
	})
	s.incrementMetric("devorbit_process_approvals_issued_total")

	writeJSON(w, http.StatusOK, map[string]any{
		"approval_token": proposal.Token,
		"workspace_id":   proposal.WorkspaceID,
		"command":        proposal.Command,
		"args":           proposal.Args,
		"reason":         proposal.Reason,
		"expires_at":     proposal.ExpiresAt.Format(time.RFC3339),
	})
}
