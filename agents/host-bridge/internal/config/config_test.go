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
