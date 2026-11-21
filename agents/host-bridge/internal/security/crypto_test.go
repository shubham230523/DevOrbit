package security

import (
	"testing"
)

func TestE2ECipher_EncryptDecryptAndTamperedPayload(t *testing.T) {
	cipherEngine := NewE2ECipher("shared-e2e-session-key-32-bytes")

	plain := []byte("Sensitive Workspace Code Data & Remote Command")
	encryptedHex, err := cipherEngine.EncryptPayload(plain)
	if err != nil {
		t.Fatalf("Failed to encrypt payload: %v", err)
	}

	decrypted, err := cipherEngine.DecryptPayload(encryptedHex)
	if err != nil {
		t.Fatalf("Failed to decrypt payload: %v", err)
	}

	if string(decrypted) != string(plain) {
		t.Errorf("Decrypted payload mismatch! Expected '%s', got '%s'", plain, decrypted)
	}

	// Test Tampered Payload -> Decryption MUST fail
	tamperedHex := encryptedHex[:len(encryptedHex)-4] + "ffff"
	_, err = cipherEngine.DecryptPayload(tamperedHex)
	if err == nil {
		t.Errorf("Expected decryption of tampered payload to fail!")
	}
}
