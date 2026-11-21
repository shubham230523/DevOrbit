package broker

import (
	"testing"
)

func TestBroker_SessionManagement(t *testing.T) {
	b := NewBroker()

	sess := &Session{
		ID:        "sess-1",
		MachineID: "machine-1",
		DeviceID:  "device-1",
	}

	if err := b.RegisterSession(sess); err != nil {
		t.Fatalf("Failed to register session: %v", err)
	}

	if b.ActiveSessionCount() != 1 {
		t.Errorf("Expected 1 active session, got %d", b.ActiveSessionCount())
	}

	got, found := b.GetSession("sess-1")
	if !found || got.MachineID != "machine-1" {
		t.Errorf("Failed to retrieve session correctly")
	}

	b.UnregisterSession("sess-1")
	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected 0 sessions after unregistering, got %d", b.ActiveSessionCount())
	}
}
