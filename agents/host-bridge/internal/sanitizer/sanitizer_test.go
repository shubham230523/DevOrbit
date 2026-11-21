package sanitizer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathSanitizer_ValidPaths(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	subFile := filepath.Join(tempDir, "src", "MainActivity.kt")
	_ = os.MkdirAll(filepath.Dir(subFile), 0755)
	_ = os.WriteFile(subFile, []byte("package com.example"), 0644)

	ps := NewPathSanitizer([]string{tempDir})

	resolved, err := ps.ResolveAndValidate(subFile)
	if err != nil {
		t.Fatalf("Expected valid path resolution, got error: %v", err)
	}

	if resolved == "" {
		t.Errorf("Expected non-empty resolved path")
	}
}

func TestPathSanitizer_PathTraversalRejection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ps := NewPathSanitizer([]string{tempDir})

	traversalPath := filepath.Join(tempDir, "..", "..", "etc", "passwd")
	_, err = ps.ResolveAndValidate(traversalPath)
	if err == nil {
		t.Errorf("Expected path traversal attack to be rejected, but it was allowed!")
	}
}
