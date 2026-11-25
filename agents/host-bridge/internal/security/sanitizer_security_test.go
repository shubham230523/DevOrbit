package security

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceJail_PathTraversalAttacks(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_jail_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	jail := NewWorkspaceJail([]string{tempRoot})

	// Test 1: Valid subpath
	subFile := filepath.Join(tempRoot, "src", "MainActivity.kt")
	_ = os.MkdirAll(filepath.Dir(subFile), 0755)
	_ = os.WriteFile(subFile, []byte("class MainActivity"), 0644)

	res, err := jail.ResolveAndValidate(tempRoot, "src/MainActivity.kt")
	if err != nil {
		t.Fatalf("Expected valid path to pass, got: %v", err)
	}
	if res == "" {
		t.Errorf("Expected non-empty path result")
	}

	// Test 2: Parent directory traversal escape
	_, err = jail.ResolveAndValidate(tempRoot, "../../Windows/System32")
	if err == nil {
		t.Errorf("Security flaw! Traversal escaping workspace root was permitted")
	}

	// Test 3: Absolute path escape outside allowed root
	outsidePath := filepath.Join(os.TempDir(), "secret.txt")
	_, err = jail.ResolveAndValidate(tempRoot, outsidePath)
	if err == nil {
		t.Errorf("Security flaw! Absolute path outside root was permitted")
	}
}

func TestWorkspaceJail_ConcurrentAccess(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_jail_concurrent_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	jail := NewWorkspaceJail([]string{tempRoot})

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				wsID := fmt.Sprintf("ws-dyn-%d", id)
				jail.AddRoot(wsID, tempRoot)
				_, _ = jail.GetRoot(wsID)
				_ = jail.ListAllowedRoots()
				_, _ = jail.ResolveAndValidate(tempRoot, "some_file.txt")
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestWorkspaceJail_SymlinkEscapeNonExistentTarget(t *testing.T) {
	tempJail, err := os.MkdirTemp("", "devorbit_jail_symlink_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempJail)

	outsideDir, err := os.MkdirTemp("", "devorbit_outside_*")
	if err != nil {
		t.Fatalf("Failed to create outside temp dir: %v", err)
	}
	defer os.RemoveAll(outsideDir)

	symlinkPath := filepath.Join(tempJail, "outside_symlink")
	err = os.Symlink(outsideDir, symlinkPath)
	if err != nil {
		t.Skipf("Symlink creation not supported in this OS environment: %v", err)
		return
	}

	jail := NewWorkspaceJail([]string{tempJail})

	// Target file does not exist inside outsideDir
	escapedPath := "outside_symlink/non_existent_file.txt"
	_, err = jail.ResolveAndValidate(tempJail, escapedPath)
	if err == nil {
		t.Errorf("Security flaw! Symlink escape via parent directory on non-existent target was permitted")
	}
}

func TestWorkspaceJail_NestedSymlinksAndCaseHandling(t *testing.T) {
	tempJail, err := os.MkdirTemp("", "devorbit_jail_nested_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempJail)

	subDir := filepath.Join(tempJail, "sub")
	_ = os.MkdirAll(subDir, 0755)

	jail := NewWorkspaceJail([]string{tempJail})

	// Case sensitivity / slash normalization test
	relPath := "SUB/../sub/./file.txt"
	resolved, err := jail.ResolveAndValidate(tempJail, relPath)
	if err != nil {
		t.Fatalf("Expected valid normalized relative path to resolve, got: %v", err)
	}
	if resolved == "" {
		t.Errorf("Expected non-empty resolved path")
	}

	// Workspace root addition & resolution test
	dynRoot, err := os.MkdirTemp("", "devorbit_jail_dyn_*")
	if err != nil {
		t.Fatalf("Failed to create dyn temp dir: %v", err)
	}
	defer os.RemoveAll(dynRoot)

	added := jail.AddRoot("ws-dyn-99", dynRoot)
	if added == "" {
		t.Errorf("AddRoot returned empty string")
	}

	resDyn, err := jail.ResolveAndValidate("ws-dyn-99", "file.txt")
	if err != nil {
		t.Fatalf("Failed to resolve path under dynamically added root: %v", err)
	}
	if resDyn == "" {
		t.Errorf("Expected non-empty resolution under dynamic root")
	}
}
