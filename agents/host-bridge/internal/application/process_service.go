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

func (s *ProcessService) GetProcessStatus(id string) (*domain.ProcessInfo, bool) {
	return s.runner.GetProcessStatus(id)
}

func (s *ProcessService) CancelProcess(id string) bool {
	return s.runner.CancelProcess(id)
}
