package pairing

import (
	"testing"
	"time"
)

func TestPairing_LifecycleAndSecurity(t *testing.T) {
	svc := NewService("master-secret-key-32-bytes!!")

	// 1. Create challenge
	ch, err := svc.CreateChallenge("host-windows-01", "Desktop-Workstation", 5*time.Minute)
	if err != nil {
		t.Fatalf("Failed to create pairing challenge: %v", err)
	}

	proofSig := svc.GenerateProof(ch.Payload, "phone-android-01")

	// 2. Confirm valid pairing
	dev, err := svc.ConfirmPairing(ch.ID, "phone-android-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
	if err != nil {
		t.Fatalf("Failed to confirm pairing: %v", err)
	}

	if dev.State != DeviceStateActive {
		t.Errorf("Expected active device state")
	}

	if !svc.IsDeviceAuthorized("host-windows-01", "phone-android-01") {
		t.Errorf("Expected device to be authorized")
	}

	// 3. Attempt replaying consumed challenge -> Should be rejected
	_, err = svc.ConfirmPairing(ch.ID, "phone-android-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
	if err != ErrChallengeUsed {
		t.Errorf("Expected ErrChallengeUsed, got: %v", err)
	}

	// 4. Revoke device
	if err := svc.RevokeDevice("host-windows-01", "phone-android-01"); err != nil {
		t.Fatalf("Failed to revoke device: %v", err)
	}

	if svc.IsDeviceAuthorized("host-windows-01", "phone-android-01") {
		t.Errorf("Expected revoked device to be unauthorized!")
	}
}

func TestPairing_ExpiredChallengeRejection(t *testing.T) {
	svc := NewService("master-secret-key-32-bytes!!")

	ch, err := svc.CreateChallenge("host-windows-01", "Desktop-Workstation", -1*time.Second)
	if err != nil {
		t.Fatalf("Failed to create challenge: %v", err)
	}

	proofSig := svc.GenerateProof(ch.Payload, "phone-android-01")

	_, err = svc.ConfirmPairing(ch.ID, "phone-android-01", "Pixel 8 Pro", "pubkey-12345", proofSig)
	if err != ErrChallengeExpired {
		t.Errorf("Expected ErrChallengeExpired, got: %v", err)
	}
}
