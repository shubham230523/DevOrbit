package broker

import (
	"errors"
	"sync"
)

type Session struct {
	ID        string
	MachineID string
	DeviceID  string
}

type Broker struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewBroker() *Broker {
	return &Broker{
		sessions: make(map[string]*Session),
	}
}

func (b *Broker) RegisterSession(session *Session) error {
	if session == nil || session.ID == "" {
		return errors.New("invalid session")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.sessions[session.ID] = session
	return nil
}

func (b *Broker) UnregisterSession(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.sessions, id)
}

func (b *Broker) GetSession(id string) (*Session, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	sess, ok := b.sessions[id]
	return sess, ok
}

func (b *Broker) ActiveSessionCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return len(b.sessions)
}
