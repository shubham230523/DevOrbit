package auth

import (
	"testing"
)

func TestAuthService_TokenLifecycle(t *testing.T) {
	svc := NewService("test-secret-key-32-bytes-long!")

	token, err := svc.GenerateToken("machine-dev-01", "device-pixel-01")
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	machineID, deviceID, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate valid token: %v", err)
	}

	if machineID != "machine-dev-01" {
		t.Errorf("Expected machineID 'machine-dev-01', got '%s'", machineID)
	}
	if deviceID != "device-pixel-01" {
		t.Errorf("Expected deviceID 'device-pixel-01', got '%s'", deviceID)
	}
}

func TestAuthService_InvalidToken(t *testing.T) {
	svc := NewService("test-secret-key-32-bytes-long!")

	_, _, err := svc.ValidateToken("invalid.token.structure")
	if err == nil {
		t.Errorf("Expected error for invalid token structure")
	}

	_, _, err = svc.ValidateToken("machine:device:12345.tampered_signature")
	if err == nil {
		t.Errorf("Expected error for tampered signature")
	}
}
