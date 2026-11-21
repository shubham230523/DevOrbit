package discovery

import (
	"os"
	"path/filepath"
)

type WorkspaceInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	IsAndroidProject bool   `json:"is_android_project"`
	HasGradle        bool   `json:"has_gradle"`
}

type WorkspaceScanner struct{}

func NewWorkspaceScanner() *WorkspaceScanner {
	return &WorkspaceScanner{}
}

func (s *WorkspaceScanner) InspectPath(path string) (*WorkspaceInfo, error) {
	cleanPath := filepath.Clean(path)
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		cleanPath = filepath.Dir(cleanPath)
	}

	name := filepath.Base(cleanPath)
	hasGradle := false
	hasAndroidManifest := false

	// Check for build.gradle or settings.gradle or build.gradle.kts
	if _, err := os.Stat(filepath.Join(cleanPath, "build.gradle")); err == nil {
		hasGradle = true
	}
	if _, err := os.Stat(filepath.Join(cleanPath, "build.gradle.kts")); err == nil {
		hasGradle = true
	}
	if _, err := os.Stat(filepath.Join(cleanPath, "settings.gradle.kts")); err == nil {
		hasGradle = true
	}

	// Check for AndroidManifest.xml inside app/src/main/
	if _, err := os.Stat(filepath.Join(cleanPath, "app", "src", "main", "AndroidManifest.xml")); err == nil {
		hasAndroidManifest = true
	}

	return &WorkspaceInfo{
		ID:               name,
		Name:             name,
		Path:             cleanPath,
		IsAndroidProject: hasAndroidManifest || (hasGradle && hasAndroidManifest),
		HasGradle:        hasGradle,
	}, nil
}
