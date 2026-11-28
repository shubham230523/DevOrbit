package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
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
	ts1 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonce1 := "nonce-pairing-attempt-0001"

	// Prove that a proof created with persistent host secret is REJECTED (and does NOT pollute nonce cache)
	authData := ConstructProofAuthenticatedDataV11("1.1", ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr, ts1, nonce1)
	persistentMac := hmac.New(sha256.New, []byte(persistentSecret))
	persistentMac.Write([]byte(authData))
	persistentProof := hex.EncodeToString(persistentMac.Sum(nil))

	_, _, _, err = engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", ts1, nonce1, persistentProof)
	if err != ErrInvalidProof {
		t.Fatalf("Security failure: expected ErrInvalidProof for proof generated with persistent secret, got: %v", err)
	}

	// Valid ephemeral proof (reusing nonce1 proves failed HMAC did not consume nonce)
	proofSig, err := engine.GenerateProofForChallenge(ch.ID, deviceID, ts1, nonce1)
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	// Confirm with valid ephemeral proof
	token, _, _, err := engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", ts1, nonce1, proofSig)
	if err != nil {
		t.Fatalf("Failed to confirm pairing: %v", err)
	}
	if token == "" {
		t.Fatalf("Expected non-empty token")
	}

	// Replaying the EXACT same (ts1, nonce1, proofSig) must be REJECTED (anti-replay)
	_, _, _, err = engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", ts1, nonce1, proofSig)
	if err != ErrReplayDetected {
		t.Fatalf("Expected ErrReplayDetected for replayed captured confirm request, got: %v", err)
	}

	// Legitimate retry by SAME device with FRESH timestamp + unseen nonce + valid v1.1 HMAC
	// returns the CACHED token (before session is activated/revoked).
	ts2 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonce2 := "nonce-pairing-retry-0002"
	proofSig2, err := engine.GenerateProofForChallenge(ch.ID, deviceID, ts2, nonce2)
	if err != nil {
		t.Fatalf("Failed to generate retry proof: %v", err)
	}
	replayToken, _, _, err := engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", ts2, nonce2, proofSig2)
	if err != nil {
		t.Fatalf("Expected same-device retry with fresh nonce to succeed, got error: %v", err)
	}
	if replayToken != token {
		t.Fatalf("Expected retry to return the same cached token, got different value")
	}

	// Replay by a DIFFERENT device even with fresh proof must be rejected with ErrPairingUsed.
	ts3 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonce3 := "nonce-other-device-0003"
	_, _, _, err = engine.ConfirmPairing(ch.ID, "different-device-99", "Other Phone", ts3, nonce3, proofSig2)
	if err != ErrPairingUsed {
		t.Fatalf("Expected ErrPairingUsed for different device replay, got: %v", err)
	}

	// Once the session is bound and invalidated (used or revoked), same-device retry closes permanently.
	engine.BindChallengeSession(ch.ID, "sess-bound-01")
	engine.InvalidateSessionRetry("sess-bound-01")

	ts4 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonce4 := "nonce-after-invalidation-0004"
	proofSig4 := GenerateProofV11(ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr, ts4, nonce4, "wrong-or-wiped")
	_ = proofSig4
	_, _, _, err = engine.ConfirmPairing(ch.ID, deviceID, "Pixel 9", ts4, nonce4, proofSig2)
	if err != ErrPairingUsed {
		t.Fatalf("Expected ErrPairingUsed after session activation/revocation invalidated retry window, got: %v", err)
	}
}

func TestHostBridge_PairingExpiredChallenge(t *testing.T) {
	engine := NewPairingEngine("host-windows-01", "Developer PC", "secret-32b-key")

	ch, err := engine.CreateChallenge(-1 * time.Second)
	if err != nil {
		t.Fatalf("Failed to create expired challenge: %v", err)
	}

	ts := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonce := "nonce-expired-0001"
	proofSig, err := engine.GenerateProofForChallenge(ch.ID, "dev-01", ts, nonce)
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	_, _, _, err = engine.ConfirmPairing(ch.ID, "dev-01", "Pixel", ts, nonce, proofSig)
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

	const concurrentAttempts = 10
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	// All 10 goroutines use the SAME device ID, each with its own fresh nonce + proof.
	// After the first confirm, subsequent calls return the cached token —
	// all 10 must succeed (idempotent retry for same device).
	for i := 0; i < concurrentAttempts; i++ {
		ts := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
		nonce := fmt.Sprintf("nonce-concurrent-%04d", i)
		proofSig, err := engine.GenerateProofForChallenge(ch.ID, "dev-01", ts, nonce)
		if err != nil {
			t.Fatalf("Failed to generate proof: %v", err)
		}

		wg.Add(1)
		go func(tStr, nStr, sig string) {
			defer wg.Done()
			_, _, _, err := engine.ConfirmPairing(ch.ID, "dev-01", "Pixel", tStr, nStr, sig)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else {
				failCount++
			}
		}(ts, nonce, proofSig)
	}
	wg.Wait()

	if successCount != concurrentAttempts {
		t.Errorf("Expected all %d same-device attempts to succeed via fresh-nonce retry, got: %d successes, %d failures",
			concurrentAttempts, successCount, failCount)
	}

	// A DIFFERENT device must still be rejected.
	tsOther := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	_, _, _, err = engine.ConfirmPairing(ch.ID, "other-device", "iPad", tsOther, "nonce-other-0001", "invalid")
	if err != ErrPairingUsed {
		t.Errorf("Expected ErrPairingUsed for a different device, got: %v", err)
	}
}
