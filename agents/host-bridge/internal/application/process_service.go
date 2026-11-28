package application

import (
	"context"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
)

type ProcessService struct {
	runner *process.ProcessRunner
}

func NewProcessService(runner *process.ProcessRunner) *ProcessService {
	return &ProcessService{runner: runner}
}

func (s *ProcessService) ExecuteProcess(ctx context.Context, spec *domain.ProcessSpec) (*domain.ProcessInfo, error) {
	return s.runner.ExecuteProcess(ctx, spec)
}

// RequiresApproval reports whether executing the spec needs an explicit user authorization decision
// in addition to session permissions. The rule lives in the shared command policy so every entry
// point (HTTP and agent tool execution) enforces the same requirement.
func (s *ProcessService) RequiresApproval(spec *domain.ProcessSpec) (bool, string) {
	return s.runner.RequiresApproval(spec)
}

func (s *ProcessService) GetProcessStatus(id string) (*domain.ProcessInfo, bool) {
	return s.runner.GetProcessStatus(id)
}

func (s *ProcessService) CancelProcess(id string) bool {
	return s.runner.CancelProcess(id)
}
