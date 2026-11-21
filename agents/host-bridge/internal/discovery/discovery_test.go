package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceScanner_AndroidProjectDetection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "devorbit_android_proj_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manifestPath := filepath.Join(tempDir, "app", "src", "main", "AndroidManifest.xml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		t.Fatalf("Failed to create dir structure: %v", err)
	}
	_ = os.WriteFile(manifestPath, []byte("<manifest></manifest>"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "build.gradle.kts"), []byte("// gradle"), 0644)

	scanner := NewWorkspaceScanner()
	ws, err := scanner.InspectPath(tempDir)
	if err != nil {
		t.Fatalf("Failed to inspect path: %v", err)
	}

	if !ws.IsAndroidProject {
		t.Errorf("Expected project to be detected as Android project")
	}

	if !ws.HasGradle {
		t.Errorf("Expected project to have Gradle")
	}
}
