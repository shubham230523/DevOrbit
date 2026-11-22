package config

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrMissingSecret           = errors.New("config error: required secret key is missing or too short")
	ErrInsecureSchemeProd      = errors.New("config error: production mode requires wss:// or https:// endpoints")
	ErrInvalidConfig           = errors.New("config error: invalid configuration parameter")
	ErrMismatchedBrokerService = errors.New("config error: broker HTTP and WebSocket endpoints must identify the same service")
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
	BrokerURL       string // Legacy / general broker WS URL
	BrokerHTTPURL   string // HTTP URL for internal broker authorization / revocation
	BrokerWSURL     string // WebSocket URL for host/client connections
	BrokerSecret    string // Service-to-service admin secret
	AdvertisedURL   string
	TrustedProxies  []string
	Environment     string
	GeminiAPIKey    string // Host-only: DEVORBIT_GEMINI_API_KEY
	GeminiModel     string // Host-only: DEVORBIT_GEMINI_MODEL
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

	// 1. Separate Broker HTTP and WebSocket Endpoints
	brokerHTTPURL := os.Getenv("DEVORBIT_BROKER_HTTP_URL")
	brokerWSURL := os.Getenv("DEVORBIT_BROKER_WS_URL")
	legacyBrokerURL := os.Getenv("DEVORBIT_BROKER_URL")

	if brokerWSURL == "" && legacyBrokerURL != "" {
		brokerWSURL = legacyBrokerURL
	}

	if env == "production" {
		if brokerHTTPURL == "" {
			return nil, fmt.Errorf("%w: DEVORBIT_BROKER_HTTP_URL is required in production mode", ErrInvalidConfig)
		}
		if brokerWSURL == "" {
			return nil, fmt.Errorf("%w: DEVORBIT_BROKER_WS_URL is required in production mode", ErrInvalidConfig)
		}
	} else {
		if brokerHTTPURL == "" {
			brokerHTTPURL = "http://127.0.0.1:8081"
		}
		if brokerWSURL == "" {
			brokerWSURL = "ws://127.0.0.1:8081"
		}
	}

	// Validate Broker HTTP URL
	httpParsed, err := url.Parse(brokerHTTPURL)
	if err != nil || httpParsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid DEVORBIT_BROKER_HTTP_URL: %v", ErrInvalidConfig, err)
	}
	if httpParsed.User != nil {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_HTTP_URL must not contain embedded credentials", ErrInvalidConfig)
	}
	if httpParsed.RawQuery != "" || httpParsed.Fragment != "" {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_HTTP_URL must not contain query parameters or fragments", ErrInvalidConfig)
	}
	if env == "production" && strings.ToLower(httpParsed.Scheme) != "https" {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_HTTP_URL '%s' must use https:// in production", ErrInsecureSchemeProd, brokerHTTPURL)
	}
	if env != "production" && strings.ToLower(httpParsed.Scheme) != "http" && strings.ToLower(httpParsed.Scheme) != "https" {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_HTTP_URL must use http:// or https://", ErrInvalidConfig)
	}

	// Validate Broker WebSocket URL
	wsParsed, err := url.Parse(brokerWSURL)
	if err != nil || wsParsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid DEVORBIT_BROKER_WS_URL: %v", ErrInvalidConfig, err)
	}
	if wsParsed.User != nil {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_WS_URL must not contain embedded credentials", ErrInvalidConfig)
	}
	if wsParsed.RawQuery != "" || wsParsed.Fragment != "" {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_WS_URL must not contain query parameters or fragments", ErrInvalidConfig)
	}
	if env == "production" && strings.ToLower(wsParsed.Scheme) != "wss" {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_WS_URL '%s' must use wss:// in production", ErrInsecureSchemeProd, brokerWSURL)
	}
	if env != "production" && strings.ToLower(wsParsed.Scheme) != "ws" && strings.ToLower(wsParsed.Scheme) != "wss" {
		return nil, fmt.Errorf("%w: DEVORBIT_BROKER_WS_URL must use ws:// or wss://", ErrInvalidConfig)
	}

	// Verify both URLs identify the same configured broker service
	if httpParsed.Hostname() != wsParsed.Hostname() {
		return nil, fmt.Errorf("%w: hostnames do not match (%s vs %s)", ErrMismatchedBrokerService, httpParsed.Hostname(), wsParsed.Hostname())
	}

	// 2. Broker Administration Secret
	brokerSecret := os.Getenv("DEVORBIT_BROKER_SECRET")
	if env == "production" {
		if brokerSecret == "" {
			return nil, fmt.Errorf("%w: DEVORBIT_BROKER_SECRET is required in production mode", ErrMissingSecret)
		}
		if len(brokerSecret) < 16 {
			return nil, fmt.Errorf("%w: DEVORBIT_BROKER_SECRET must be at least 16 characters in production", ErrMissingSecret)
		}
		if brokerSecret == hostSecret {
			return nil, fmt.Errorf("%w: DEVORBIT_BROKER_SECRET cannot be identical to host-wide secret", ErrInvalidConfig)
		}
	} else if brokerSecret == "" {
		brokerSecret = hostSecret
	}

	// 3. Advertised URL Validation
	advertisedURL := os.Getenv("DEVORBIT_ADVERTISED_URL")
	if env == "production" {
		if advertisedURL == "" {
			return nil, fmt.Errorf("%w: DEVORBIT_ADVERTISED_URL is required in production mode", ErrInvalidConfig)
		}
		advParsed, err := url.Parse(advertisedURL)
		if err != nil || advParsed.Host == "" {
			return nil, fmt.Errorf("%w: invalid DEVORBIT_ADVERTISED_URL: %v", ErrInvalidConfig, err)
		}
		if advParsed.User != nil {
			return nil, fmt.Errorf("%w: DEVORBIT_ADVERTISED_URL must not contain credentials", ErrInvalidConfig)
		}
		if strings.ToLower(advParsed.Scheme) != "https" {
			return nil, fmt.Errorf("%w: DEVORBIT_ADVERTISED_URL '%s' must use https:// in production mode", ErrInsecureSchemeProd, advertisedURL)
		}
	} else if advertisedURL != "" {
		advParsed, err := url.Parse(advertisedURL)
		if err != nil || advParsed.Host == "" {
			return nil, fmt.Errorf("%w: invalid DEVORBIT_ADVERTISED_URL: %v", ErrInvalidConfig, err)
		}
		if advParsed.User != nil {
			return nil, fmt.Errorf("%w: DEVORBIT_ADVERTISED_URL must not contain credentials", ErrInvalidConfig)
		}
		sch := strings.ToLower(advParsed.Scheme)
		if sch != "http" && sch != "https" {
			return nil, fmt.Errorf("%w: DEVORBIT_ADVERTISED_URL must use http:// or https://", ErrInvalidConfig)
		}
	}

	// 4. Trusted Reverse Proxies
	var trustedProxies []string
	if rawProxies := os.Getenv("DEVORBIT_TRUSTED_PROXIES"); rawProxies != "" {
		for _, p := range strings.Split(rawProxies, ",") {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				trustedProxies = append(trustedProxies, trimmed)
			}
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

	geminiKey := strings.TrimSpace(os.Getenv("DEVORBIT_GEMINI_API_KEY"))
	if geminiKey == "" {
		geminiKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	}
	geminiModel := strings.TrimSpace(os.Getenv("DEVORBIT_GEMINI_MODEL"))
	if geminiModel == "" {
		geminiModel = "gemini-2.5-flash"
	}

	return &Config{
		AllowedRoots:    roots,
		Port:            port,
		LogLevel:        "info",
		MaxFileSizeMB:   maxSize,
		HostID:          hostID,
		HostName:        hostName,
		HostSecret:      hostSecret,
		BrokerURL:       brokerWSURL,
		BrokerHTTPURL:   brokerHTTPURL,
		BrokerWSURL:     brokerWSURL,
		BrokerSecret:    brokerSecret,
		AdvertisedURL:   advertisedURL,
		TrustedProxies:  trustedProxies,
		Environment:     env,
		GeminiAPIKey:    geminiKey,
		GeminiModel:     geminiModel,
		AllowedBinaries: []string{
			"git", "git.exe", "gradlew", "gradlew.bat", "gradle", "gradle.bat", "adb", "adb.exe",
		},
	}, nil
}
