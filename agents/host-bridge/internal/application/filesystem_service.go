package application

import (
	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
)

type FilesystemService struct {
	storage *filesystem.StorageEngine
}

func NewFilesystemService(storage *filesystem.StorageEngine) *FilesystemService {
	return &FilesystemService{storage: storage}
}

func (s *FilesystemService) ListDirectory(workspaceID, relativePath string) (*domain.DirectoryListing, error) {
	return s.storage.ListDirectory(workspaceID, relativePath)
}

func (s *FilesystemService) ReadFile(workspaceID, relativePath string) (*domain.ReadFileResult, error) {
	return s.storage.ReadFile(workspaceID, relativePath)
}

func (s *FilesystemService) WriteFile(req *domain.WriteFileRequest) (*domain.WriteFileResult, error) {
	return s.storage.WriteFile(req)
}
