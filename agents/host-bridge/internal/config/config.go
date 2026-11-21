package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrMissingSecret      = errors.New("config error: required secret key is missing or too short")
	ErrInsecureSchemeProd = errors.New("config error: production mode requires wss:// or https:// endpoints")
)

type Config struct {
	AllowedRoots    []string
	Port            int
	LogLevel        string
	MaxFileSizeMB   int64
	AllowedBinaries []string
	HostID          string
	HostName        string
	HostSecret      string
	BrokerURL       string
	Environment     string
}

func LoadConfig() (*Config, error) {
	env := strings.ToLower(os.Getenv("DEVORBIT_ENV"))
	if env == "" {
		env = "development"
	}

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

	hostName := os.Getenv("DEVORBIT_HOST_NAME")
	if hostName == "" {
		hostName, _ = os.Hostname()
		if hostName == "" {
			hostName = "Desktop Workstation"
		}
	}

	hostID := os.Getenv("DEVORBIT_HOST_ID")
	if hostID == "" {
		sanitizedHost := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			if r >= 'A' && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return '-'
		}, hostName)
		sanitizedHost = strings.Trim(sanitizedHost, "-")
		if sanitizedHost == "" {
			sanitizedHost = "node"
		}
		hostID = fmt.Sprintf("host-%s", sanitizedHost)
	}

	hostSecret := os.Getenv("DEVORBIT_HOST_SECRET")
	if hostSecret == "" {
		if env == "production" {
			return nil, fmt.Errorf("%w: DEVORBIT_HOST_SECRET is required in production mode", ErrMissingSecret)
		}
		hostSecret = "devorbit-host-bridge-master-secret-key-32b"
		log.Println("Config Warning: Using default development host secret. Set DEVORBIT_HOST_SECRET in production.")
	} else if len(hostSecret) < 16 {
		return nil, fmt.Errorf("%w: DEVORBIT_HOST_SECRET must be at least 16 characters", ErrMissingSecret)
	}

	brokerURL := os.Getenv("DEVORBIT_BROKER_URL")
	if brokerURL == "" {
		brokerURL = "ws://localhost:8081"
	}

	if env == "production" && !strings.HasPrefix(brokerURL, "wss://") && !strings.HasPrefix(brokerURL, "https://") {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_URL '%s' must use wss:// or https:// in production mode", ErrInsecureSchemeProd, brokerURL)
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
		HostID:        hostID,
		HostName:      hostName,
		HostSecret:    hostSecret,
		BrokerURL:     brokerURL,
		Environment:   env,
		AllowedBinaries: []string{
			"git", "git.exe", "gradlew", "gradlew.bat", "gradle", "gradle.bat", "adb", "adb.exe",
		},
	}, nil
}
