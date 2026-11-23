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

	// Replay by SAME device returns the CACHED token (idempotent retry support).
	// This allows the mobile client to recover when the first response was lost
	// in transit (e.g. network timeout before the HTTP response arrived).
	replayToken, err := engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", "pubkey-123", proofSig)
	if err != nil {
		t.Fatalf("Expected cached-token replay to succeed for same device, got error: %v", err)
	}
	if replayToken != token {
		t.Fatalf("Expected replay to return the same cached token, got different value")
	}

	// Replay by a DIFFERENT device must still be rejected with ErrPairingUsed.
	_, err = engine.ConfirmPairing(ch.ID, "different-device-99", "Other Phone", "", proofSig)
	if err != ErrPairingUsed {
		t.Fatalf("Expected ErrPairingUsed for different device replay, got: %v", err)
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
	failCount := 0
	var mu sync.Mutex

	// All 10 goroutines use the SAME device ID and valid proof.
	// After the first confirm, subsequent calls return the cached token —
	// all 10 must succeed (idempotent replay for same device).
	for i := 0; i < concurrentAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.ConfirmPairing(ch.ID, "dev-01", "Pixel", "pubkey", proofSig)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				failCount++
			}
		}()
	}
	wg.Wait()

	if successCount != concurrentAttempts {
		t.Errorf("Expected all %d same-device attempts to succeed via cached replay, got: %d successes, %d failures",
			concurrentAttempts, successCount, failCount)
	}

	// A DIFFERENT device must still be rejected.
	_, err = engine.ConfirmPairing(ch.ID, "other-device", "iPad", "", proofSig)
	if err != ErrPairingUsed {
		t.Errorf("Expected ErrPairingUsed for a different device, got: %v", err)
	}
}
