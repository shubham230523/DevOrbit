package adb

import (
	"context"
	"testing"
)

func TestADBClient_ListDevices(t *testing.T) {
	client := NewADBClient("adb")

	devices, err := client.ListDevices(context.Background())
	if err != nil {
		// Hardware dependent test: if adb binary is not in system PATH or daemon not running, skip gracefully
		t.Skipf("ADB binary not available or ADB daemon offline: %v", err)
	}

	if devices == nil {
		t.Errorf("Expected non-nil slice of ADB devices")
	}
}
