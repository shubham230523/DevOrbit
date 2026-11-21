package application

import (
	"context"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
)

type GitService struct {
	client *git.GitClient
}

func NewGitService(client *git.GitClient) *GitService {
	return &GitService{client: client}
}

func (s *GitService) GetStatus(ctx context.Context, workspaceID string) (*domain.GitStatus, error) {
	return s.client.GetStatus(ctx, workspaceID)
}

func (s *GitService) GetDiff(ctx context.Context, workspaceID string) (*domain.GitDiff, error) {
	return s.client.GetDiff(ctx, workspaceID)
}
