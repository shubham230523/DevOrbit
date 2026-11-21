package domain

import "errors"

var (
	ErrAccessDenied     = errors.New("access denied: path outside allowed workspace boundaries")
	ErrFileNotFound     = errors.New("file not found")
	ErrFileTooLarge     = errors.New("file size exceeds maximum allowed read limit")
	ErrConflictDetected = errors.New("conflict detected: file modified on host since last read")
)

type FileEntry struct {
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	IsDirectory  bool   `json:"is_directory"`
	SizeBytes    int64  `json:"size_bytes"`
	ModTime      string `json:"mod_time"`
}

type DirectoryListing struct {
	RelativePath string      `json:"relative_path"`
	Entries      []FileEntry `json:"entries"`
}

type ReadFileResult struct {
	RelativePath string `json:"relative_path"`
	Content      string `json:"content"`
	SHA256Hash   string `json:"sha256_hash"`
	IsBinary     bool   `json:"is_binary"`
	SizeBytes    int64  `json:"size_bytes"`
}

type WriteFileRequest struct {
	WorkspaceID  string `json:"workspace_id"`
	RelativePath string `json:"relative_path"`
	NewContent   string `json:"new_content"`
	BaseSHA256   string `json:"base_sha256"`
}

type WriteFileResult struct {
	RelativePath  string `json:"relative_path"`
	UpdatedSHA256 string `json:"updated_sha256"`
}

type ConflictError struct {
	Message        string `json:"error"`
	CurrentContent string `json:"current_content"`
	CurrentSHA256  string `json:"current_sha256"`
}

func (e *ConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return ErrConflictDetected.Error()
}

type FileEvent struct {
	Op           string `json:"op"` // "create", "write", "remove", "rename"
	RelativePath string `json:"relative_path"`
}
