package filesystem

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
	"github.com/fsnotify/fsnotify"
)

const MaxFileSizeDefault int64 = 10 * 1024 * 1024 // 10 MB limit

type StorageEngine struct {
	jail        *security.WorkspaceJail
	maxFileSize int64
}

func NewStorageEngine(jail *security.WorkspaceJail, maxFileSize int64) *StorageEngine {
	if maxFileSize <= 0 {
		maxFileSize = MaxFileSizeDefault
	}
	return &StorageEngine{
		jail:        jail,
		maxFileSize: maxFileSize,
	}
}

func (s *StorageEngine) ListDirectory(workspaceID, relativePath string) (*domain.DirectoryListing, error) {
	absPath, err := s.jail.ResolveAndValidate(workspaceID, relativePath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	resultEntries := make([]domain.FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		entryRelPath := filepath.Join(relativePath, entry.Name())
		resultEntries = append(resultEntries, domain.FileEntry{
			Name:         entry.Name(),
			RelativePath: entryRelPath,
			IsDirectory:  entry.IsDir(),
			SizeBytes:    info.Size(),
			ModTime:      info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return &domain.DirectoryListing{
		RelativePath: relativePath,
		Entries:      resultEntries,
	}, nil
}

func (s *StorageEngine) ReadFile(workspaceID, relativePath string) (*domain.ReadFileResult, error) {
	absPath, err := s.jail.ResolveAndValidate(workspaceID, relativePath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, domain.ErrFileNotFound
	}

	if info.Size() > s.maxFileSize {
		return nil, domain.ErrFileTooLarge
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])
	isBinary := !utf8.Valid(data)

	contentStr := ""
	if !isBinary {
		contentStr = string(data)
	}

	return &domain.ReadFileResult{
		RelativePath: relativePath,
		Content:      contentStr,
		SHA256Hash:   hashStr,
		IsBinary:     isBinary,
		SizeBytes:    info.Size(),
	}, nil
}

func (s *StorageEngine) WriteFile(req *domain.WriteFileRequest) (*domain.WriteFileResult, error) {
	absPath, err := s.jail.ResolveAndValidate(req.WorkspaceID, req.RelativePath)
	if err != nil {
		return nil, err
	}

	// Conflict Detection Check if file already exists
	if _, err := os.Stat(absPath); err == nil {
		existingData, err := os.ReadFile(absPath)
		if err == nil {
			existingHash := sha256.Sum256(existingData)
			existingHashStr := hex.EncodeToString(existingHash[:])

			if req.BaseSHA256 != "" && req.BaseSHA256 != existingHashStr {
				return nil, &domain.ConflictError{
					Message:        domain.ErrConflictDetected.Error(),
					CurrentContent: string(existingData),
					CurrentSHA256:  existingHashStr,
				}
			}
		}
	}

	// Atomic Write: write to temporary file, then rename
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directories: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".devorbit_tmp_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for atomic write: %w", err)
	}
	tmpName := tmpFile.Name()

	newBytes := []byte(req.NewContent)
	if _, err := tmpFile.Write(newBytes); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return nil, err
	}
	tmpFile.Close()

	if err := os.Rename(tmpName, absPath); err != nil {
		os.Remove(tmpName)
		return nil, fmt.Errorf("failed to replace target file atomically: %w", err)
	}

	newHash := sha256.Sum256(newBytes)
	newHashStr := hex.EncodeToString(newHash[:])

	return &domain.WriteFileResult{
		RelativePath:  req.RelativePath,
		UpdatedSHA256: newHashStr,
	}, nil
}

func (s *StorageEngine) CreateWatcher(workspaceID, rootPath string) (*fsnotify.Watcher, error) {
	absPath, err := s.jail.ResolveAndValidate(workspaceID, rootPath)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})

	if err != nil {
		watcher.Close()
		return nil, err
	}

	return watcher, nil
}

func ComputeSHA256(reader io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
