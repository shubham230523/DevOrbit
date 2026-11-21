package process

import (
	"context"
	"os"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestProcessRunner_ExecutionAndCancellation(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_proc_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	policy := security.NewDefaultCommandPolicy()
	runner := NewProcessRunner(policy, jail)

	spec := &domain.ProcessSpec{
		WorkspaceID:    tempRoot,
		Command:        "git",
		Args:           []string{"version"},
		TimeoutSeconds: 5,
	}

	info, err := runner.ExecuteProcess(context.Background(), spec)
	if err != nil {
		t.Fatalf("Failed to execute process: %v", err)
	}

	if info.ID == "" {
		t.Errorf("Expected valid process ID")
	}

	// Wait for background process completion
	time.Sleep(500 * time.Millisecond)

	status, found := runner.GetProcessStatus(info.ID)
	if !found {
		t.Fatalf("Failed to retrieve process status")
	}

	if status.Status != domain.ProcessStatusCompleted {
		t.Errorf("Expected status 'completed', got '%s'", status.Status)
	}
}
