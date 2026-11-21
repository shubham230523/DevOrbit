package broker

import (
	"errors"
	"log"
	"net/http"
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024 * 1024,
	WriteBufferSize: 1024 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

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
	Timestamp       int64  `json:"timestamp"`
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
	authorizedHost map[string]string             // Key: MachineID -> Expected Token
}

func NewBroker() *Broker {
	return &Broker{
		sessions:       make(map[string]*Session),
		hostSessions:   make(map[string]map[string]*Session),
		authorizedDev:  make(map[string]bool),
		authorizedHost: make(map[string]string),
	}
}

func (b *Broker) AuthorizeHost(machineID, token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authorizedHost[machineID] = token
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

func (b *Broker) RegisterHostConnection(machineID, sessionID, token string, conn *websocket.Conn) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Validate Host Authentication
	if expectedToken, ok := b.authorizedHost[machineID]; ok && expectedToken != "" {
		if token != expectedToken {
			return nil, ErrUnauthorizedSession
		}
	}

	sess, ok := b.sessions[sessionID]
	if !ok {
		sess = &Session{
			ID:        sessionID,
			MachineID: machineID,
			Token:     token,
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}
		b.sessions[sessionID] = sess
	}

	if sess.HostConn != nil && sess.HostConn != conn {
		// Close existing stale host connection
		_ = sess.HostConn.Close()
	}
	sess.HostConn = conn

	if _, ok := b.hostSessions[machineID]; !ok {
		b.hostSessions[machineID] = make(map[string]*Session)
	}
	b.hostSessions[machineID][sessionID] = sess

	return sess, nil
}

func (b *Broker) RegisterClientConnection(machineID, deviceID, sessionID, token string, conn *websocket.Conn) (*Session, error) {
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

	if sess.ClientConn != nil && sess.ClientConn != conn {
		_ = sess.ClientConn.Close()
	}

	sess.DeviceID = deviceID
	sess.ClientConn = conn

	return sess, nil
}

func (b *Broker) HandleHostWebSocket(w http.ResponseWriter, r *http.Request) {
	machineID := r.URL.Query().Get("machine_id")
	sessionID := r.URL.Query().Get("session_id")
	token := r.URL.Query().Get("token")

	if machineID == "" || sessionID == "" {
		http.Error(w, "missing machine_id or session_id query param", http.StatusBadRequest)
		return
	}

	b.mu.RLock()
	expectedToken, hostEnforced := b.authorizedHost[machineID]
	b.mu.RUnlock()

	if hostEnforced && expectedToken != "" && token != expectedToken {
		http.Error(w, "unauthorized host authentication token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade host WebSocket: %v", err)
		return
	}

	sess, err := b.RegisterHostConnection(machineID, sessionID, token, conn)
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}
	log.Printf("Broker: Authenticated Host WSS connected for machine=%s session=%s", machineID, sessionID)

	defer b.CloseSession(sess.ID)

	for {
		var frame FrameEnvelope
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		// Relay frame directly from Host to Client without decrypting payload
		_ = b.RelayFrameFromHostToClient(sessionID, &frame)
	}
}

func (b *Broker) HandleClientWebSocket(w http.ResponseWriter, r *http.Request) {
	machineID := r.URL.Query().Get("machine_id")
	deviceID := r.URL.Query().Get("device_id")
	sessionID := r.URL.Query().Get("session_id")
	token := r.URL.Query().Get("token")

	if machineID == "" || deviceID == "" || sessionID == "" {
		http.Error(w, "missing machine_id, device_id, or session_id", http.StatusBadRequest)
		return
	}

	b.mu.RLock()
	isAuthorized := b.authorizedDev[machineID+":"+deviceID]
	b.mu.RUnlock()

	if !isAuthorized {
		http.Error(w, "unauthorized device identity or revoked trust", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade client WebSocket: %v", err)
		return
	}

	sess, err := b.RegisterClientConnection(machineID, deviceID, sessionID, token, conn)
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}
	log.Printf("Broker: Authenticated Client WSS connected for machine=%s device=%s session=%s", machineID, deviceID, sessionID)

	defer b.CloseSession(sess.ID)

	for {
		var frame FrameEnvelope
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		// Relay frame directly from Client to Host without decrypting payload
		_ = b.RelayFrameFromClientToHost(sessionID, &frame)
	}
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
