package broker

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrSessionNotFound     = errors.New("session not found or host offline")
	ErrUnauthorizedSession = errors.New("unauthorized connection or invalid session token")
	ErrFrameTooLarge       = errors.New("frame size exceeds broker maximum limit")
)

const MaxFrameSizeBytes = 10 * 1024 * 1024 // 10 MB

type ConnectionMode string

const (
	ModeLAN      ConnectionMode = "lan"
	ModeInternet ConnectionMode = "internet"
)

type FrameEnvelope struct {
	ProtocolVersion string `json:"v"`
	SessionID       string `json:"session_id"`
	Type            string `json:"type"` // "data", "ping", "pong", "close"
	EncryptedData   string `json:"encrypted_payload"`
	SeqNum          int64  `json:"seq_num"`
}

type Session struct {
	ID         string
	MachineID  string
	DeviceID   string
	Token      string
	Mode       ConnectionMode
	ExpiresAt  time.Time
	HostConn   *websocket.Conn
	ClientConn *websocket.Conn
	mu         sync.Mutex
}

type Broker struct {
	mu            sync.RWMutex
	sessions      map[string]*Session            // Key: SessionID
	hostSessions  map[string]map[string]*Session // Key: MachineID -> map[SessionID]*Session
	authorizedDev map[string]bool                // Key: MachineID + ":" + DeviceID
}

func NewBroker() *Broker {
	return &Broker{
		sessions:      make(map[string]*Session),
		hostSessions:  make(map[string]map[string]*Session),
		authorizedDev: make(map[string]bool),
	}
}

func (b *Broker) AuthorizeDevice(machineID, deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authorizedDev[machineID+":"+deviceID] = true
}

func (b *Broker) RevokeDevice(machineID, deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.authorizedDev, machineID+":"+deviceID)

	// Terminate active sessions for revoked device
	for _, sess := range b.sessions {
		if sess.MachineID == machineID && sess.DeviceID == deviceID {
			b.closeSessionLocked(sess.ID)
		}
	}
}

func (b *Broker) RegisterHostConnection(machineID, sessionID string, conn *websocket.Conn) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sess, ok := b.sessions[sessionID]
	if !ok {
		sess = &Session{
			ID:        sessionID,
			MachineID: machineID,
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}
		b.sessions[sessionID] = sess
	}

	sess.HostConn = conn

	if _, ok := b.hostSessions[machineID]; !ok {
		b.hostSessions[machineID] = make(map[string]*Session)
	}
	b.hostSessions[machineID][sessionID] = sess

	return sess, nil
}

func (b *Broker) RegisterClientConnection(machineID, deviceID, sessionID string, conn *websocket.Conn) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.authorizedDev[machineID+":"+deviceID] {
		return nil, ErrUnauthorizedSession
	}

	sess, ok := b.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	if time.Now().After(sess.ExpiresAt) {
		delete(b.sessions, sessionID)
		return nil, errors.New("session expired")
	}

	sess.DeviceID = deviceID
	sess.ClientConn = conn

	return sess, nil
}

func (b *Broker) RelayFrameFromClientToHost(sessionID string, frame *FrameEnvelope) error {
	b.mu.RLock()
	sess, ok := b.sessions[sessionID]
	b.mu.RUnlock()

	if !ok || sess.HostConn == nil {
		return ErrSessionNotFound
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	return sess.HostConn.WriteJSON(frame)
}

func (b *Broker) RelayFrameFromHostToClient(sessionID string, frame *FrameEnvelope) error {
	b.mu.RLock()
	sess, ok := b.sessions[sessionID]
	b.mu.RUnlock()

	if !ok || sess.ClientConn == nil {
		return ErrSessionNotFound
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	return sess.ClientConn.WriteJSON(frame)
}

func (b *Broker) CloseSession(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeSessionLocked(sessionID)
}

func (b *Broker) closeSessionLocked(sessionID string) {
	sess, ok := b.sessions[sessionID]
	if !ok {
		return
	}

	if sess.HostConn != nil {
		_ = sess.HostConn.Close()
	}
	if sess.ClientConn != nil {
		_ = sess.ClientConn.Close()
	}

	if m, ok := b.hostSessions[sess.MachineID]; ok {
		delete(m, sessionID)
		if len(m) == 0 {
			delete(b.hostSessions, sess.MachineID)
		}
	}

	delete(b.sessions, sessionID)
}

func (b *Broker) ActiveSessionCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.sessions)
}
