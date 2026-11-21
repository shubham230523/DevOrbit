package pairing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"
)

func TestPairing_LifecycleAndSecurity(t *testing.T) {
	persistentSecret := "master-secret-key-32-bytes!!"
	svc := NewService(persistentSecret)

	// 1. Create challenge
	ch, err := svc.CreateChallenge("mach-control-01", "Desktop-Workstation", 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to create pairing challenge: %v", err)
	}

	// Prove that a proof created with the persistent server secret is REJECTED
	authData := ConstructProofAuthenticatedData(ch.Version, ch.ID, ch.Payload, ch.MachineID, "device-pixel-01", ch.ExpiresAtStr)
	persistentMac := hmac.New(sha256.New, []byte(persistentSecret))
	persistentMac.Write([]byte(authData))
	persistentProof := hex.EncodeToString(persistentMac.Sum(nil))

	_, err = svc.ConfirmPairing(ch.ID, "device-pixel-01", "Pixel 8 Pro", "pubkey-12345", persistentProof)
	if err != ErrInvalidProof {
		t.Fatalf("Security failure: expected ErrInvalidProof for proof generated with persistent secret, got: %v", err)
	}

	// Valid ephemeral proof
	proofSig, err := svc.GenerateProofForChallenge(ch.ID, "device-pixel-01")
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	// 2. Confirm valid pairing
	dev, err := svc.ConfirmPairing(ch.ID, "device-pixel-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
	if err != nil {
		t.Fatalf("Failed to confirm pairing: %v", err)
	}

	if dev.State != DeviceStateActive {
		t.Errorf("Expected active device state")
	}

	if !svc.IsDeviceAuthorized("mach-control-01", "device-pixel-01") {
		t.Errorf("Expected device to be authorized")
	}

	// 3. Attempt replaying consumed challenge -> Should be rejected
	_, err = svc.ConfirmPairing(ch.ID, "device-pixel-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
	if err != ErrChallengeUsed {
		t.Errorf("Expected ErrChallengeUsed, got: %v", err)
	}

	// 4. Revoke device
	if err := svc.RevokeDevice("mach-control-01", "device-pixel-01"); err != nil {
		t.Fatalf("Failed to revoke device: %v", err)
	}

	if svc.IsDeviceAuthorized("mach-control-01", "device-pixel-01") {
		t.Errorf("Expected revoked device to be unauthorized!")
	}
}

func TestPairing_ExpiredChallengeRejection(t *testing.T) {
	svc := NewService("master-secret-key-32-bytes!!")

	ch, err := svc.CreateChallenge("mach-control-01", "Desktop-Workstation", -1*time.Second)
	if err != nil {
		t.Fatalf("Failed to create challenge: %v", err)
	}

	proofSig, err := svc.GenerateProofForChallenge(ch.ID, "device-pixel-01")
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}

	_, err = svc.ConfirmPairing(ch.ID, "device-pixel-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
	if err != ErrChallengeExpired {
		t.Errorf("Expected ErrChallengeExpired, got: %v", err)
	}
}

func TestPairing_ConcurrentConfirmationOnlyOneSucceeds(t *testing.T) {
	svc := NewService("master-secret-key-32-bytes!!")

	ch, err := svc.CreateChallenge("mach-control-01", "Desktop-Workstation", 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to create challenge: %v", err)
	}

	proofSig, err := svc.GenerateProofForChallenge(ch.ID, "device-pixel-01")
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
			_, err := svc.ConfirmPairing(ch.ID, "device-pixel-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if err == ErrChallengeUsed {
				usedCount++
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful confirmation under concurrency, got: %d", successCount)
	}
	if usedCount != concurrentAttempts-1 {
		t.Errorf("Expected %d attempts to fail with ErrChallengeUsed, got: %d", concurrentAttempts-1, usedCount)
	}
}
