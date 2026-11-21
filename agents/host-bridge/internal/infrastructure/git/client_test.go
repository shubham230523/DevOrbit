package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"devorbit.net/agents/host-bridge/internal/security"
)

func TestGitClient_StatusAndDiff(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_git_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	// Initialize git repo in temp dir
	cmdInit := exec.Command("git", "init")
	cmdInit.Dir = tempRoot
	if err := cmdInit.Run(); err != nil {
		t.Skipf("Git executable not available for git integration test: %v", err)
	}

	_ = os.WriteFile(filepath.Join(tempRoot, "test.txt"), []byte("git test content"), 0644)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	client := NewGitClient(jail)

	status, err := client.GetStatus(context.Background(), tempRoot)
	if err != nil {
		t.Fatalf("Failed to get git status: %v", err)
	}

	if status.IsClean {
		t.Errorf("Expected git repo with untracked file to be unclean")
	}

	if len(status.UntrackedFiles) == 0 {
		t.Errorf("Expected untracked file 'test.txt' in git status")
	}

	diff, err := client.GetDiff(context.Background(), tempRoot)
	if err != nil {
		t.Fatalf("Failed to get git diff: %v", err)
	}

	if diff == nil {
		t.Errorf("Expected non-nil diff response")
	}
}
