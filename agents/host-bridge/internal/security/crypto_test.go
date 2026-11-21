package security

import (
	"bytes"
	"testing"
	"time"
)

func TestE2ESession_HKDFKeyDerivationAndDirectionalSeparation(t *testing.T) {
	session, err := NewE2ESession("sess-100", "device-phone-01", "shared-master-secret-32-bytes", 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to initialize E2E session: %v", err)
	}

	if bytes.Equal(session.ClientToHostKey, session.HostToClientKey) {
		t.Fatalf("Security failure! Client-to-Host key must be distinct from Host-to-Client key")
	}

	plain := []byte("Sensitive Code Edit Command")
	seqNum := int64(1)
	ts := time.Now().UnixNano()

	// Encrypt Client -> Host
	cipherHex, err := session.Encrypt(DirClientToHost, plain, seqNum, ts)
	if err != nil {
		t.Fatalf("Failed to encrypt client payload: %v", err)
	}

	// Decrypt Client -> Host -> Should succeed
	decrypted, err := session.Decrypt(DirClientToHost, cipherHex, seqNum, ts)
	if err != nil {
		t.Fatalf("Failed to decrypt payload: %v", err)
	}

	if string(decrypted) != string(plain) {
		t.Errorf("Decrypted text mismatch")
	}
}

func TestE2ESession_ReplayAttackRejection(t *testing.T) {
	session, err := NewE2ESession("sess-200", "device-phone-01", "shared-master-secret-32-bytes", 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to initialize E2E session: %v", err)
	}

	plain := []byte("Run Gradle Build")
	seqNum := int64(10)
	ts := time.Now().UnixNano()

	cipherHex, _ := session.Encrypt(DirClientToHost, plain, seqNum, ts)

	// First decryption -> Success
	_, err = session.Decrypt(DirClientToHost, cipherHex, seqNum, ts)
	if err != nil {
		t.Fatalf("First decryption failed: %v", err)
	}

	// Second decryption with same sequence number (Replay Attack) -> MUST fail
	_, err = session.Decrypt(DirClientToHost, cipherHex, seqNum, ts)
	if err != ErrReplayedFrame {
		t.Errorf("Expected ErrReplayedFrame for replayed sequence number, got: %v", err)
	}
}

func TestE2ESession_AADMetadataTamperRejection(t *testing.T) {
	session, err := NewE2ESession("sess-300", "device-phone-01", "shared-master-secret-32-bytes", 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to initialize E2E session: %v", err)
	}

	plain := []byte("ReadFile(app/build.gradle)")
	seqNum := int64(1)
	ts := time.Now().UnixNano()

	cipherHex, _ := session.Encrypt(DirClientToHost, plain, seqNum, ts)

	// Attempt decrypting with modified AAD timestamp -> MUST fail
	tamperedTs := ts + 1000
	_, err = session.Decrypt(DirClientToHost, cipherHex, seqNum, tamperedTs)
	if err == nil {
		t.Errorf("Security flaw! Decryption succeeded despite modified AAD metadata")
	}
}

func TestE2ESession_ExpiredKeyDestruction(t *testing.T) {
	session, err := NewE2ESession("sess-400", "device-phone-01", "shared-master-secret-32-bytes", -1*time.Second)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	_, err = session.Encrypt(DirClientToHost, []byte("test"), 1, time.Now().UnixNano())
	if err != ErrExpiredSessionKey {
		t.Errorf("Expected ErrExpiredSessionKey for expired key, got: %v", err)
	}

	session.Destroy()
	for _, b := range session.ClientToHostKey {
		if b != 0 {
			t.Errorf("Expected ClientToHostKey memory bytes to be zeroed out")
		}
	}
}
