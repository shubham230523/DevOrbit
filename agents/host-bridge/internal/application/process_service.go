package application

import (
	"context"
	"fmt"
	"sync"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
)

// ProcessService is the single execution boundary for the Host Bridge.
//
// Both the direct HTTP process endpoint and the agent `execute_command` tool call ExecuteProcess, so
// the approval gate below cannot be bypassed by choosing another route. A route that forgets to set
// the owner or to present an execution token fails closed for commands that require approval.
type ProcessService struct {
	runner *process.ProcessRunner

	approvalMu    sync.RWMutex
	approvalStore *ProcessApprovalStore
}

func NewProcessService(runner *process.ProcessRunner) *ProcessService {
	return &ProcessService{runner: runner}
}

// SetProcessApprovalStore installs the authoritative approval service. Until it is configured,
// commands that require approval are refused.
func (s *ProcessService) SetProcessApprovalStore(store *ProcessApprovalStore) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.approvalStore = store
}

func (s *ProcessService) processApprovalStore() *ProcessApprovalStore {
	s.approvalMu.RLock()
	defer s.approvalMu.RUnlock()
	return s.approvalStore
}

// RequiresApproval reports whether executing the spec needs an explicit user authorization decision
// in addition to session permissions. The rule lives in the shared command policy.
func (s *ProcessService) RequiresApproval(spec *domain.ProcessSpec) (bool, string) {
	return s.runner.RequiresApproval(spec)
}

// ExecuteProcess authorizes and then executes the spec.
//
// For commands that run project-defined code (the Gradle family) the authorization is consumed here,
// at the single boundary both routes share, immediately before launch:
//   - the approval store must be configured (fail closed otherwise);
//   - the session and device that own the approval must be known;
//   - the token must be unused, unexpired, and match the exact workspace, command and arguments.
func (s *ProcessService) ExecuteProcess(ctx context.Context, spec *domain.ProcessSpec) (*domain.ProcessInfo, error) {
	if spec == nil {
		return nil, fmt.Errorf("process specification is required")
	}

	if requiresApproval, _ := s.runner.RequiresApproval(spec); requiresApproval {
		store := s.processApprovalStore()
		if store == nil {
			return nil, ErrApprovalRequirementMissing
		}
		if spec.OwnerSessionID == "" || spec.OwnerDeviceID == "" {
			return nil, ErrProcessApprovalOwner
		}
		if _, err := store.ConsumeAuthorization(
			spec.ApprovalToken, spec.OwnerSessionID, spec.OwnerDeviceID,
			spec.WorkspaceID, spec.Command, spec.Args,
		); err != nil {
			return nil, err
		}
		// The token must not survive into execution, responses or logs.
		spec.ApprovalToken = ""
	}

	return s.runner.ExecuteProcess(ctx, spec)
}

func (s *ProcessService) GetProcessStatus(id string) (*domain.ProcessInfo, bool) {
	return s.runner.GetProcessStatus(id)
}

func (s *ProcessService) CancelProcess(id string) bool {
	return s.runner.CancelProcess(id)
}
