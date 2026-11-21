package broker

import (
	"testing"
)

func TestBroker_AuthorizationAndDeviceRevocation(t *testing.T) {
	b := NewBroker()

	// 1. Attempt registering client connection before device authorization -> Should fail
	_, err := b.RegisterClientConnection("machine-win-01", "phone-android-01", "sess-100", nil)
	if err != ErrUnauthorizedSession {
		t.Errorf("Expected ErrUnauthorizedSession, got: %v", err)
	}

	// 2. Authorize device on broker
	b.AuthorizeDevice("machine-win-01", "phone-android-01")

	// 3. Register host connection
	_, err = b.RegisterHostConnection("machine-win-01", "sess-100", nil)
	if err != nil {
		t.Fatalf("Failed to register host connection: %v", err)
	}

	// 4. Register client connection -> Should succeed
	sess, err := b.RegisterClientConnection("machine-win-01", "phone-android-01", "sess-100", nil)
	if err != nil {
		t.Fatalf("Failed to register client connection: %v", err)
	}

	if sess.MachineID != "machine-win-01" || sess.DeviceID != "phone-android-01" {
		t.Errorf("Session metadata mismatch")
	}

	if b.ActiveSessionCount() != 1 {
		t.Errorf("Expected 1 active session, got %d", b.ActiveSessionCount())
	}

	// 5. Revoke device -> Active session must be closed
	b.RevokeDevice("machine-win-01", "phone-android-01")

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected 0 active sessions after device revocation, got %d", b.ActiveSessionCount())
	}
}
