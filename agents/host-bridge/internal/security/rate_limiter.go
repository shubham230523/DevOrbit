package security

import (
	"errors"
	"sync"
	"time"
)

var ErrRateLimitExceeded = errors.New("rate limit exceeded: too many failed pairing attempts")

type RateLimiter struct {
	mu           sync.RWMutex
	attempts     map[string]int
	lockouts     map[string]time.Time
	maxAttempts  int
	lockoutDuration time.Duration
}

func NewRateLimiter(maxAttempts int, lockoutDuration time.Duration) *RateLimiter {
	return &RateLimiter{
		attempts:        make(map[string]int),
		lockouts:        make(map[string]time.Time),
		maxAttempts:     maxAttempts,
		lockoutDuration: lockoutDuration,
	}
}

func (r *RateLimiter) Allow(clientKey string) error {
	r.mu.RLock()
	lockoutTime, locked := r.lockouts[clientKey]
	r.mu.RUnlock()

	if locked {
		if time.Now().Before(lockoutTime) {
			return ErrRateLimitExceeded
		}
		// Lockout expired
		r.mu.Lock()
		delete(r.lockouts, clientKey)
		delete(r.attempts, clientKey)
		r.mu.Unlock()
	}

	return nil
}

func (r *RateLimiter) RecordFailure(clientKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.attempts[clientKey]++
	if r.attempts[clientKey] >= r.maxAttempts {
		r.lockouts[clientKey] = time.Now().Add(r.lockoutDuration)
	}
}

func (r *RateLimiter) RecordSuccess(clientKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.attempts, clientKey)
	delete(r.lockouts, clientKey)
}
