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
	os.Setenv("DEVORBIT_BROKER_URL", "ws://unencrypted-broker.com:8081")
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")
	defer os.Unsetenv("DEVORBIT_BROKER_URL")

	_, err := LoadConfig()
	if err == nil {
		t.Errorf("Expected error when using insecure ws:// broker URL in production mode")
	}
}
