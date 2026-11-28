package config

import (
	"os"
	"testing"
)

func TestConfig_ProductionValidation(t *testing.T) {
	os.Setenv("DEVORBIT_ENV", "production")
	os.Setenv("DEVORBIT_HOST_SECRET", "short")
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")

	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected config load error for weak production secret (<16 chars)")
	}
}

func TestConfig_InsecureBrokerURLInProduction(t *testing.T) {
	os.Setenv("DEVORBIT_ENV", "production")
	os.Setenv("DEVORBIT_HOST_SECRET", "valid-production-secret-key-32-bytes")
	os.Setenv("DEVORBIT_BROKER_HTTP_URL", "http://unencrypted-broker.com")
	os.Setenv("DEVORBIT_BROKER_WS_URL", "wss://unencrypted-broker.com")
	os.Setenv("DEVORBIT_BROKER_SECRET", "valid-broker-secret-32-bytes-ok!")
	os.Setenv("DEVORBIT_ADVERTISED_URL", "https://host.example.com")
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")
	defer os.Unsetenv("DEVORBIT_BROKER_HTTP_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_WS_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_SECRET")
	defer os.Unsetenv("DEVORBIT_ADVERTISED_URL")

	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected error when using insecure http:// broker URL in production mode")
	}
}

func TestConfig_MismatchedBrokerServiceInProduction(t *testing.T) {
	os.Setenv("DEVORBIT_ENV", "production")
	os.Setenv("DEVORBIT_HOST_SECRET", "valid-production-secret-key-32-bytes")
	os.Setenv("DEVORBIT_BROKER_HTTP_URL", "https://broker-a.example.com")
	os.Setenv("DEVORBIT_BROKER_WS_URL", "wss://broker-b.example.com")
	os.Setenv("DEVORBIT_BROKER_SECRET", "valid-broker-secret-32-bytes-ok!")
	os.Setenv("DEVORBIT_ADVERTISED_URL", "https://host.example.com")
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")
	defer os.Unsetenv("DEVORBIT_BROKER_HTTP_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_WS_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_SECRET")
	defer os.Unsetenv("DEVORBIT_ADVERTISED_URL")

	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected error when broker HTTP and WS URLs point to different hosts")
	}
}

func TestConfig_MissingAdvertisedURLInProduction(t *testing.T) {
	os.Setenv("DEVORBIT_ENV", "production")
	os.Setenv("DEVORBIT_HOST_SECRET", "valid-production-secret-key-32-bytes")
	os.Setenv("DEVORBIT_BROKER_HTTP_URL", "https://broker.example.com")
	os.Setenv("DEVORBIT_BROKER_WS_URL", "wss://broker.example.com")
	os.Setenv("DEVORBIT_BROKER_SECRET", "valid-broker-secret-32-bytes-ok!")
	os.Unsetenv("DEVORBIT_ADVERTISED_URL")
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")
	defer os.Unsetenv("DEVORBIT_BROKER_HTTP_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_WS_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_SECRET")

	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected error when DEVORBIT_ADVERTISED_URL is missing in production")
	}
}

func TestConfig_IdenticalBrokerSecretAndHostSecretRejectedInProduction(t *testing.T) {
	os.Setenv("DEVORBIT_ENV", "production")
	os.Setenv("DEVORBIT_HOST_SECRET", "shared-identical-secret-key-32b!")
	os.Setenv("DEVORBIT_BROKER_HTTP_URL", "https://broker.example.com")
	os.Setenv("DEVORBIT_BROKER_WS_URL", "wss://broker.example.com")
	os.Setenv("DEVORBIT_BROKER_SECRET", "shared-identical-secret-key-32b!")
	os.Setenv("DEVORBIT_ADVERTISED_URL", "https://host.example.com")
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")
	defer os.Unsetenv("DEVORBIT_BROKER_HTTP_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_WS_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_SECRET")
	defer os.Unsetenv("DEVORBIT_ADVERTISED_URL")

	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected error when DEVORBIT_BROKER_SECRET is identical to DEVORBIT_HOST_SECRET in production")
	}
}

func TestConfig_DevelopmentDefaults(t *testing.T) {
	os.Setenv("DEVORBIT_ENV", "development")
	os.Unsetenv("DEVORBIT_BROKER_HTTP_URL")
	os.Unsetenv("DEVORBIT_BROKER_WS_URL")
	os.Unsetenv("DEVORBIT_BROKER_URL")
	os.Unsetenv("DEVORBIT_ADVERTISED_URL")
	defer os.Unsetenv("DEVORBIT_ENV")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Unexpected error loading dev config: %v", err)
	}

	if cfg.BrokerHTTPURL != "http://127.0.0.1:8081" {
		t.Errorf("Expected dev BrokerHTTPURL to be http://127.0.0.1:8081, got %s", cfg.BrokerHTTPURL)
	}
	if cfg.BrokerWSURL != "ws://127.0.0.1:8081" {
		t.Errorf("Expected dev BrokerWSURL to be ws://127.0.0.1:8081, got %s", cfg.BrokerWSURL)
	}
}

func TestConfig_GeminiConfiguration(t *testing.T) {
	t.Setenv("DEVORBIT_ENV", "development")
	t.Setenv("DEVORBIT_GEMINI_API_KEY", "test-secret-gemini-key-12345")
	t.Setenv("DEVORBIT_GEMINI_MODEL", "gemini-2.5-pro")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("Unexpected error loading config: %v", err)
	}

	if cfg.GeminiAPIKey != "test-secret-gemini-key-12345" {
		t.Errorf("Expected GeminiAPIKey to be loaded, got %s", cfg.GeminiAPIKey)
	}
	if cfg.GeminiModel != "gemini-2.5-pro" {
		t.Errorf("Expected GeminiModel to be gemini-2.5-pro, got %s", cfg.GeminiModel)
	}
}

func setBaseProdEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DEVORBIT_ENV", "production")
	t.Setenv("DEVORBIT_HOST_SECRET", "valid-production-secret-key-32-bytes")
	t.Setenv("DEVORBIT_BROKER_HTTP_URL", "https://broker.example.com")
	t.Setenv("DEVORBIT_BROKER_WS_URL", "wss://broker.example.com")
	t.Setenv("DEVORBIT_BROKER_SECRET", "valid-broker-secret-32-bytes-ok!")
	t.Setenv("DEVORBIT_ADVERTISED_URL", "https://host.example.com")
	t.Setenv("DEVORBIT_TLS_CERT_FILE", "")
	t.Setenv("DEVORBIT_TLS_KEY_FILE", "")
	t.Setenv("DEVORBIT_TRUSTED_PROXIES", "")
	t.Setenv("DEVORBIT_BIND_ADDR", "")
}

func TestConfig_ProductionListenerAndTLSValidation(t *testing.T) {
	t.Run("rejects production without native TLS or trusted proxies", func(t *testing.T) {
		setBaseProdEnv(t)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("expected error when neither TLS nor trusted proxies are configured in production")
		}
	})

	t.Run("rejects partial TLS cert without key", func(t *testing.T) {
		setBaseProdEnv(t)
		t.Setenv("DEVORBIT_TLS_CERT_FILE", "/etc/ssl/cert.pem")
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("expected error when DEVORBIT_TLS_CERT_FILE is set without DEVORBIT_TLS_KEY_FILE")
		}
	})

	t.Run("rejects non-loopback bind addr without native TLS in production", func(t *testing.T) {
		setBaseProdEnv(t)
		t.Setenv("DEVORBIT_TRUSTED_PROXIES", "127.0.0.1")
		t.Setenv("DEVORBIT_BIND_ADDR", "0.0.0.0")
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("expected error when DEVORBIT_BIND_ADDR is non-loopback without native TLS in production")
		}
	})

	t.Run("defaults BindAddr to loopback when using trusted proxy without native TLS", func(t *testing.T) {
		setBaseProdEnv(t)
		t.Setenv("DEVORBIT_TRUSTED_PROXIES", "127.0.0.1")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BindAddr != "127.0.0.1" {
			t.Fatalf("expected BindAddr=127.0.0.1 behind trusted proxy, got %q", cfg.BindAddr)
		}
	})

	t.Run("allows explicit IPv6 loopback behind trusted proxy", func(t *testing.T) {
		setBaseProdEnv(t)
		t.Setenv("DEVORBIT_TRUSTED_PROXIES", "::1")
		t.Setenv("DEVORBIT_BIND_ADDR", "::1")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BindAddr != "::1" {
			t.Fatalf("expected BindAddr=::1, got %q", cfg.BindAddr)
		}
	})

	t.Run("defaults BindAddr to 0.0.0.0 when native TLS cert and key are configured", func(t *testing.T) {
		setBaseProdEnv(t)
		t.Setenv("DEVORBIT_TLS_CERT_FILE", "/etc/ssl/host.crt")
		t.Setenv("DEVORBIT_TLS_KEY_FILE", "/etc/ssl/host.key")
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BindAddr != "0.0.0.0" {
			t.Fatalf("expected BindAddr=0.0.0.0 with native TLS, got %q", cfg.BindAddr)
		}
	})

	t.Run("IsLoopbackAddr checks", func(t *testing.T) {
		for _, addr := range []string{"127.0.0.1", "::1", "[::1]", "localhost", "127.0.1.1"} {
			if !IsLoopbackAddr(addr) {
				t.Errorf("expected IsLoopbackAddr(%q) == true", addr)
			}
		}
		for _, addr := range []string{"", "0.0.0.0", "192.168.1.10", "::", "example.com"} {
			if IsLoopbackAddr(addr) {
				t.Errorf("expected IsLoopbackAddr(%q) == false", addr)
			}
		}
	})
}
