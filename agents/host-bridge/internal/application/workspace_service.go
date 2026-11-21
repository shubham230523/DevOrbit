package application

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

type WorkspaceService struct {
	jail *security.WorkspaceJail
	mu   sync.RWMutex
}

func NewWorkspaceService(jail *security.WorkspaceJail) *WorkspaceService {
	return &WorkspaceService{jail: jail}
}

func (s *WorkspaceService) ListWorkspaces() ([]domain.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspaces := []domain.Workspace{}
	seen := make(map[string]bool)

	for id, rootPath := range s.jail.ListAllowedRoots() {
		if seen[rootPath] {
			continue
		}
		seen[rootPath] = true

		info, err := s.InspectWorkspace(id, rootPath)
		if err == nil {
			workspaces = append(workspaces, *info)
		}
	}

	return workspaces, nil
}

func (s *WorkspaceService) InspectWorkspace(id, rootPath string) (*domain.Workspace, error) {
	name := filepath.Base(rootPath)
	hasGradle := false
	hasAndroidManifest := false
	hasGit := false

	if _, err := os.Stat(filepath.Join(rootPath, "build.gradle")); err == nil {
		hasGradle = true
	}
	if _, err := os.Stat(filepath.Join(rootPath, "build.gradle.kts")); err == nil {
		hasGradle = true
	}
	if _, err := os.Stat(filepath.Join(rootPath, "settings.gradle.kts")); err == nil {
		hasGradle = true
	}

	if _, err := os.Stat(filepath.Join(rootPath, "app", "src", "main", "AndroidManifest.xml")); err == nil {
		hasAndroidManifest = true
	}

	if _, err := os.Stat(filepath.Join(rootPath, ".git")); err == nil {
		hasGit = true
	}

	return &domain.Workspace{
		ID:               id,
		Name:             name,
		RootPath:         rootPath,
		IsAndroidProject: hasAndroidManifest || (hasGradle && hasAndroidManifest),
		HasGradle:        hasGradle,
		HasGit:           hasGit,
	}, nil
}

func (s *WorkspaceService) DiscoverWorkspaces(ctx context.Context, opts *domain.DiscoverOptions) ([]domain.Workspace, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 3
	}

	discovered := []domain.Workspace{}
	visited := make(map[string]bool)

	for _, root := range opts.SearchRoots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return nil
			}

			if !d.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			if err == nil {
				depth := len(filepath.SplitList(rel))
				if depth > opts.MaxDepth {
					return filepath.SkipDir
				}
			}

			// Deduplicate
			cleanPath := filepath.Clean(path)
			if visited[cleanPath] {
				return nil
			}

			ws, err := s.InspectWorkspace(cleanPath, cleanPath)
			if err == nil && (ws.HasGradle || ws.HasGit || ws.IsAndroidProject) {
				visited[cleanPath] = true
				s.jail.AddRoot(ws.ID, cleanPath)
				discovered = append(discovered, *ws)
			}

			return nil
		})
	}

	return discovered, nil
}
