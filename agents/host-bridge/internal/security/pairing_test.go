package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"
)

func TestHostBridge_PairingLifecycleAndSecurity(t *testing.T) {
	persistentSecret := "host-persistent-secret-key-32b!"
	engine := NewPairingEngine("host-windows-01", "Developer PC", persistentSecret)

	// 1. Create Challenge
	ch, err := engine.CreateChallenge(5 * time.Minute)
	if err != nil {
		t.Fatalf("Failed to create challenge: %v", err)
	}

	deviceID := "mobile-device-01"

	// Prove that a proof created with persistent host secret is REJECTED
	authData := ConstructProofAuthenticatedData(ch.Version, ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr)
	persistentMac := hmac.New(sha256.New, []byte(persistentSecret))
	persistentMac.Write([]byte(authData))
	persistentProof := hex.EncodeToString(persistentMac.Sum(nil))

	_, err = engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", "pubkey-123", persistentProof)
	if err != ErrInvalidProof {
		t.Fatalf("Security failure: expected ErrInvalidProof for proof generated with persistent secret, got: %v", err)
	}

	// Valid ephemeral proof
	proofSig, err := engine.GenerateProofForChallenge(ch.ID, deviceID)
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	// Confirm with valid ephemeral proof
	token, err := engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", "pubkey-123", proofSig)
	if err != nil {
		t.Fatalf("Failed to confirm pairing: %v", err)
	}
	if token == "" {
		t.Fatalf("Expected non-empty token")
	}

	// Replay must be rejected with ErrPairingUsed
	_, err = engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", "pubkey-123", proofSig)
	if err != ErrPairingUsed {
		t.Fatalf("Expected ErrPairingUsed on replay, got: %v", err)
	}
}

func TestHostBridge_PairingExpiredChallenge(t *testing.T) {
	engine := NewPairingEngine("host-windows-01", "Developer PC", "secret-32b-key")

	ch, err := engine.CreateChallenge(-1 * time.Second)
	if err != nil {
		t.Fatalf("Failed to create expired challenge: %v", err)
	}

	proofSig, err := engine.GenerateProofForChallenge(ch.ID, "dev-01")
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	_, err = engine.ConfirmPairing(ch.ID, "dev-01", "Pixel", "pubkey", proofSig)
	if err != ErrPairingExpired {
		t.Fatalf("Expected ErrPairingExpired, got: %v", err)
	}
}

func TestHostBridge_PairingConcurrentConfirmation(t *testing.T) {
	engine := NewPairingEngine("host-windows-01", "Developer PC", "secret-32b-key")

	ch, err := engine.CreateChallenge(5 * time.Minute)
	if err != nil {
		t.Fatalf("Failed to create challenge: %v", err)
	}

	proofSig, err := engine.GenerateProofForChallenge(ch.ID, "dev-01")
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	const concurrentAttempts = 10
	var wg sync.WaitGroup
	successCount := 0
	usedCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrentAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.ConfirmPairing(ch.ID, "dev-01", "Pixel", "pubkey", proofSig)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if err == ErrPairingUsed {
				usedCount++
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("Expected exactly 1 success under concurrency, got: %d", successCount)
	}
	if usedCount != concurrentAttempts-1 {
		t.Errorf("Expected %d attempts to fail with ErrPairingUsed, got: %d", concurrentAttempts-1, usedCount)
	}
}
