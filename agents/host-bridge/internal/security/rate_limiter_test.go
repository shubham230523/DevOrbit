package security

import (
	"testing"
	"time"
)

func TestRateLimiter_LockoutEnforcement(t *testing.T) {
	limiter := NewRateLimiter(3, 100*time.Millisecond)

	clientKey := "device-attacker-01"

	for i := 0; i < 2; i++ {
		limiter.RecordFailure(clientKey)
		if err := limiter.Allow(clientKey); err != nil {
			t.Fatalf("Expected connection allowed before reaching max failures")
		}
	}

	limiter.RecordFailure(clientKey) // 3rd failure -> Triggers lockout

	if err := limiter.Allow(clientKey); err != ErrRateLimitExceeded {
		t.Errorf("Expected ErrRateLimitExceeded after 3 failed attempts, got: %v", err)
	}

	time.Sleep(150 * time.Millisecond) // Wait for lockout expiry

	if err := limiter.Allow(clientKey); err != nil {
		t.Errorf("Expected lockout to expire, got error: %v", err)
	}
}
