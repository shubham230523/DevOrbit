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

// processApprovalTTL bounds how long a proposal stays undecided and how long an issued execution
// token stays usable.
const processApprovalTTL = 10 * time.Minute

// processApprovalStore lazily creates the store. A server without explicit configuration still
// enforces approvals: the requirement is derived from the shared command policy, never from wiring.
func (s *Server) processApprovalStore() *application.ProcessApprovalStore {
	s.approvalStoreMu.Lock()
	defer s.approvalStoreMu.Unlock()
	if s.processApprovals == nil {
		s.processApprovals = application.NewProcessApprovalStore(processApprovalTTL)
	}
	return s.processApprovals
}

// SetProcessApprovalStore replaces the store (used by tests and by main for a shared instance).
func (s *Server) SetProcessApprovalStore(store *application.ProcessApprovalStore) {
	s.approvalStoreMu.Lock()
	defer s.approvalStoreMu.Unlock()
	s.processApprovals = store
}

// isApprovalError reports whether err originates in the process approval boundary.
func isApprovalError(err error) bool {
	return errors.Is(err, application.ErrApprovalRequirementMissing) ||
		errors.Is(err, application.ErrProcessApprovalNotFound) ||
		errors.Is(err, application.ErrProcessApprovalNotPending) ||
		errors.Is(err, application.ErrProcessApprovalNotApproved) ||
		errors.Is(err, application.ErrProcessApprovalExpired) ||
		errors.Is(err, application.ErrProcessApprovalOwner) ||
		errors.Is(err, application.ErrProcessApprovalDigest) ||
		errors.Is(err, application.ErrProcessApprovalScope) ||
		errors.Is(err, application.ErrProcessApprovalToken)
}

// approvalErrorStatus maps an approval failure to its HTTP status. Everything defaults to 403: an
// unknown approval error must never be reported as success.
func approvalErrorStatus(err error) int {
	switch {
	case errors.Is(err, application.ErrProcessApprovalExpired):
		return http.StatusGone
	case errors.Is(err, application.ErrProcessApprovalNotPending):
		return http.StatusConflict
	default:
		return http.StatusForbidden
	}
}

// approvalRejectionReason maps approval errors to stable, non-sensitive reason codes.
func approvalRejectionReason(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, application.ErrApprovalRequirementMissing):
		return "approval_missing"
	case errors.Is(err, application.ErrProcessApprovalNotFound):
		return "proposal_not_found"
	case errors.Is(err, application.ErrProcessApprovalNotPending):
		return "proposal_already_decided"
	case errors.Is(err, application.ErrProcessApprovalNotApproved):
		return "proposal_pending"
	case errors.Is(err, application.ErrProcessApprovalExpired):
		return "proposal_expired"
	case errors.Is(err, application.ErrProcessApprovalOwner):
		return "owner_mismatch"
	case errors.Is(err, application.ErrProcessApprovalDigest):
		return "digest_mismatch"
	case errors.Is(err, application.ErrProcessApprovalScope):
		return "scope_mismatch"
	case errors.Is(err, application.ErrProcessApprovalToken):
		return "token_not_found_or_consumed"
	default:
		return "approval_error"
	}
}

// approvalStatus writes the fail-closed response for a rejected approval operation.
func (s *Server) approvalStatus(w http.ResponseWriter, err error, command string, argCount int) {
	s.securityEvent(observability.LevelWarn, EventProcessApprovalRejected, map[string]any{
		"command":   command,
		"arg_count": argCount,
		"reason":    approvalRejectionReason(err),
	})
	s.incrementMetric("devorbit_process_approvals_rejected_total")
	writeError(w, approvalErrorStatus(err), "approval_required: "+approvalRejectionReason(err))
}

// HandleCreateProcessApproval records a PENDING approval request.
//
// This endpoint deliberately issues no execution capability: it only tells the Host Bridge that a
// command is proposed. Separation of request from grant is the property that stops an automated or
// AI-driven caller from authorizing its own execution.
func (s *Server) HandleCreateProcessApproval(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionProcessExecute)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	required, policyReason := s.processSvc.RequiresApproval(spec)
	if !required {
		writeError(w, http.StatusBadRequest, "command does not require approval; submit it directly")
		return
	}

	approval, err := s.processApprovalStore().Create(
		authCtx.SessionID, authCtx.DeviceID,
		req.WorkspaceID, req.Command, req.Args, policyReason,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create approval proposal")
		return
	}

	s.securityEvent(observability.LevelInfo, EventProcessApprovalRequested, map[string]any{
		"approval_id": approval.ID,
		"command":     approval.Command,
		"arg_count":   len(approval.Args),
		"workspace":   approval.WorkspaceID,
		"expires_at":  approval.ExpiresAt.Format(time.RFC3339),
	})
	s.incrementMetric("devorbit_process_approvals_requested_total")

	// No execution_token: the caller must present this proposal to a human and then decide.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"approval_id":    approval.ID,
		"state":          approval.State,
		"workspace_id":   approval.WorkspaceID,
		"command":        approval.Command,
		"args":           approval.Args,
		"reason":         approval.Reason,
		"command_digest": approval.Digest,
		"expires_at":     approval.ExpiresAt.Format(time.RFC3339),
	})
}

// HandleProcessApproval handles GET (status) and POST /decision on an existing proposal.
func (s *Server) HandleProcessApproval(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := s.authorizeRequest(w, r, security.PermissionProcessExecute)
	if !ok {
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/processes/approvals/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	approvalID := parts[0]
	if approvalID == "" {
		writeError(w, http.StatusNotFound, "approval id is required")
		return
	}

	if len(parts) == 1 && r.Method == http.MethodGet {
		s.handleGetProcessApproval(w, authCtx, approvalID)
		return
	}
	if len(parts) == 2 && parts[1] == "decision" && r.Method == http.MethodPost {
		s.handleDecideProcessApproval(w, r, authCtx, approvalID)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		s.handleCancelProcessApproval(w, authCtx, approvalID)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleGetProcessApproval(w http.ResponseWriter, authCtx *AuthenticatedContext, approvalID string) {
	approval, err := s.processApprovalStore().Get(approvalID, authCtx.SessionID, authCtx.DeviceID)
	if err != nil {
		s.approvalStatus(w, err, "", 0)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id":    approval.ID,
		"state":          approval.State,
		"workspace_id":   approval.WorkspaceID,
		"command":        approval.Command,
		"args":           approval.Args,
		"reason":         approval.Reason,
		"command_digest": approval.Digest,
		"expires_at":     approval.ExpiresAt.Format(time.RFC3339),
	})
}

// handleCancelProcessApproval abandons a pending proposal on the server. Without this, a client
// "cancel" would only change its own UI while the host proposal stayed actionable until expiry.
// Nothing is authorized by cancellation and no token is ever revealed.
func (s *Server) handleCancelProcessApproval(w http.ResponseWriter, authCtx *AuthenticatedContext, approvalID string) {
	approval, err := s.processApprovalStore().Cancel(approvalID, authCtx.SessionID, authCtx.DeviceID)
	if err != nil {
		s.approvalStatus(w, err, "", 0)
		return
	}

	s.securityEvent(observability.LevelInfo, EventProcessApprovalCancelled, map[string]any{
		"approval_id": approval.ID,
		"command":     approval.Command,
		"workspace":   approval.WorkspaceID,
	})
	s.incrementMetric("devorbit_process_approvals_cancelled_total")
	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id": approval.ID,
		"state":       approval.State,
	})
}

func (s *Server) handleDecideProcessApproval(w http.ResponseWriter, r *http.Request, authCtx *AuthenticatedContext, approvalID string) {
	var req struct {
		Decision      string `json:"decision"`
		CommandDigest string `json:"command_digest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid decision JSON")
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "approve" && decision != "reject" {
		writeError(w, http.StatusBadRequest, "decision must be 'approve' or 'reject'")
		return
	}

	approval, err := s.processApprovalStore().Decide(
		approvalID, authCtx.SessionID, authCtx.DeviceID, decision, req.CommandDigest,
	)
	if err != nil {
		s.approvalStatus(w, err, "", 0)
		return
	}

	if decision == "reject" {
		s.securityEvent(observability.LevelInfo, EventProcessApprovalRejectedDecided, map[string]any{
			"approval_id": approval.ID,
			"command":     approval.Command,
			"workspace":   approval.WorkspaceID,
		})
		s.incrementMetric("devorbit_process_approvals_rejected_decided_total")
		writeJSON(w, http.StatusOK, map[string]any{
			"approval_id": approval.ID,
			"state":       approval.State,
		})
		return
	}

	// Approval recorded: the execution token is returned exactly once, to the deciding owner.
	s.securityEvent(observability.LevelInfo, EventProcessApprovalApproved, map[string]any{
		"approval_id": approval.ID,
		"command":     approval.Command,
		"arg_count":   len(approval.Args),
		"workspace":   approval.WorkspaceID,
	})
	s.incrementMetric("devorbit_process_approvals_approved_total")
	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id":     approval.ID,
		"state":           approval.State,
		"execution_token": approval.Token,
		"expires_at":      approval.ExpiresAt.Format(time.RFC3339),
	})
}
