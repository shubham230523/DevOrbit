package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	AllowedRoots   []string
	Port           int
	LogLevel       string
	MaxFileSizeMB  int64
	AllowedBinaries []string
}

func LoadConfig() *Config {
	port := 8082
	if p := os.Getenv("DEVORBIT_PORT"); p != "" {
		if val, err := strconv.Atoi(p); err == nil {
			port = val
		}
	}

	maxSize := int64(10)
	if m := os.Getenv("DEVORBIT_MAX_FILE_SIZE_MB"); m != "" {
		if val, err := strconv.ParseInt(m, 10, 64); err == nil {
			maxSize = val
		}
	}

	// Default allowed roots
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	roots := []string{cwd}
	if home != "" {
		projDir := filepath.Join(home, "projects")
		if _, err := os.Stat(projDir); err == nil {
			roots = append(roots, projDir)
		}
	}

	return &Config{
		AllowedRoots:  roots,
		Port:          port,
		LogLevel:      "info",
		MaxFileSizeMB: maxSize,
		AllowedBinaries: []string{
			"git", "git.exe", "gradlew", "gradlew.bat", "gradle", "gradle.bat", "adb", "adb.exe",
		},
	}
}
