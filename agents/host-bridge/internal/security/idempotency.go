package security

import (
	"sync"
	"time"
)

type OperationRecord struct {
	RequestID  string
	Result     any
	ExecutedAt time.Time
}

type IdempotencyManager struct {
	mu      sync.RWMutex
	records map[string]*OperationRecord
	ttl     time.Duration
}

func NewIdempotencyManager(ttl time.Duration) *IdempotencyManager {
	return &IdempotencyManager{
		records: make(map[string]*OperationRecord),
		ttl:     ttl,
	}
}

func (m *IdempotencyManager) Get(requestID string) (any, bool) {
	if requestID == "" {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, found := m.records[requestID]
	if !found {
		return nil, false
	}

	if time.Since(rec.ExecutedAt) > m.ttl {
		return nil, false
	}

	return rec.Result, true
}

func (m *IdempotencyManager) Store(requestID string, result any) {
	if requestID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.records[requestID] = &OperationRecord{
		RequestID:  requestID,
		Result:     result,
		ExecutedAt: time.Now(),
	}
}
