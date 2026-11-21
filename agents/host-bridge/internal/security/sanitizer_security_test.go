package security

import (
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
