package security

import (
	"testing"
	"time"
)

func TestIdempotencyManager_Deduplication(t *testing.T) {
	mgr := NewIdempotencyManager(10 * time.Minute)

	reqID := "req-atomic-write-001"
	expectedResult := map[string]string{"status": "written", "updated_sha256": "abc12345"}

	// First query -> Not found
	_, found := mgr.Get(reqID)
	if found {
		t.Errorf("Expected request ID to be unrecorded initially")
	}

	// Store result
	mgr.Store(reqID, expectedResult)

	// Second query (Replayed request on reconnect) -> Should return cached result
	res, found := mgr.Get(reqID)
	if !found {
		t.Fatalf("Expected recorded idempotency result to be found")
	}

	resMap, ok := res.(map[string]string)
	if !ok || resMap["status"] != "written" {
		t.Errorf("Idempotency cached result mismatch")
	}
}
