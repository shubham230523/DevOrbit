package filesystem

import (
	"os"
	"testing"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestStorageEngine_ConflictDetectionAndAtomicWrite(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_fs_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	engine := NewStorageEngine(jail, 10*1024*1024)

	relPath := "build.gradle.kts"

	// 1. Initial write
	req1 := &domain.WriteFileRequest{
		WorkspaceID:  tempRoot,
		RelativePath: relPath,
		NewContent:   "// initial content",
		BaseSHA256:   "",
	}
	res1, err := engine.WriteFile(req1)
	if err != nil {
		t.Fatalf("Failed initial file write: %v", err)
	}

	// 2. Read file to verify hash
	readRes, err := engine.ReadFile(tempRoot, relPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if readRes.SHA256Hash != res1.UpdatedSHA256 {
		t.Errorf("Mismatch in SHA256 hashes")
	}

	// 3. Attempt stale write with wrong base SHA256 -> Should trigger Conflict Error
	reqStale := &domain.WriteFileRequest{
		WorkspaceID:  tempRoot,
		RelativePath: relPath,
		NewContent:   "// stale update",
		BaseSHA256:   "invalid_sha256_hash_here",
	}
	_, err = engine.WriteFile(reqStale)
	if err == nil {
		t.Errorf("Expected stale write with mismatched base SHA256 to be rejected with Conflict Error")
	}

	// 4. Successful write with correct base SHA256
	reqValid := &domain.WriteFileRequest{
		WorkspaceID:  tempRoot,
		RelativePath: relPath,
		NewContent:   "// valid updated content",
		BaseSHA256:   readRes.SHA256Hash,
	}
	res2, err := engine.WriteFile(reqValid)
	if err != nil {
		t.Fatalf("Expected valid write to succeed, got: %v", err)
	}

	if res2.UpdatedSHA256 == readRes.SHA256Hash {
		t.Errorf("Expected updated SHA256 to change after content update")
	}
}
