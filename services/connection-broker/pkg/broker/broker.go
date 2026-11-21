package broker

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrSessionNotFound          = errors.New("session not found or host offline")
	ErrUnauthorizedSession      = errors.New("unauthorized connection or invalid session token")
	ErrFrameTooLarge            = errors.New("frame size exceeds broker maximum limit")
	ErrInvalidOrigin            = errors.New("websocket connection rejected due to untrusted origin")
	ErrQueryCredentialsRejected = errors.New("credentials supplied via URL query parameters are rejected")
	ErrMissingCredentials       = errors.New("missing authentication credentials in headers")
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
	mu                     sync.RWMutex
	sessions               map[string]*Session            // Key: SessionID
	hostSessions           map[string]map[string]*Session // Key: MachineID -> map[SessionID]*Session
	authorizedDev          map[string]bool                // Key: MachineID + ":" + DeviceID
	authorizedHost         map[string]string             // Key: MachineID -> Expected Token
	authorizedClientTokens map[string]string             // Key: MachineID + ":" + DeviceID + ":" + SessionID -> Token
	authorizedExpiries     map[string]time.Time          // Key: MachineID + ":" + DeviceID + ":" + SessionID -> Expiry
	allowedOrigins         map[string]bool                // Key: Origin domain
	environment            string
	adminSecret            string
}

func NewBroker() *Broker {
	return NewBrokerWithSecret(os.Getenv("DEVORBIT_BROKER_SECRET"))
}

func NewBrokerWithSecret(adminSecret string) *Broker {
	env := strings.ToLower(os.Getenv("DEVORBIT_ENV"))
	if env == "" {
		env = "development"
	}

	origins := map[string]bool{
		"https://app.devorbit.net": true,
	}

	if env != "production" {
		origins["http://localhost"] = true
		origins["http://localhost:8081"] = true
		origins["http://127.0.0.1"] = true
		origins["http://127.0.0.1:8081"] = true
		origins["http://10.0.2.2:8081"] = true
	}

	return &Broker{
		sessions:               make(map[string]*Session),
		hostSessions:           make(map[string]map[string]*Session),
		authorizedDev:          make(map[string]bool),
		authorizedHost:         make(map[string]string),
		authorizedClientTokens: make(map[string]string),
		authorizedExpiries:     make(map[string]time.Time),
		allowedOrigins:         origins,
		environment:            env,
		adminSecret:            adminSecret,
	}
}

func (b *Broker) AddAllowedOrigin(origin string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allowedOrigins[origin] = true
	if u, err := url.Parse(origin); err == nil && u.Scheme != "" && u.Host != "" {
		norm := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
		b.allowedOrigins[norm] = true
	}
}

func extractToken(r *http.Request) (string, error) {
	// 1. Explicitly reject credentials supplied through URL query parameters
	if r.URL.Query().Get("token") != "" || r.URL.Query().Has("token") ||
		r.URL.Query().Get("credential") != "" || r.URL.Query().Has("credential") ||
		r.URL.Query().Get("secret") != "" || r.URL.Query().Has("secret") {
		return "", ErrQueryCredentialsRejected
	}

	// 2. Check Authorization Header: Bearer <token>
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := strings.TrimSpace(parts[1])
			if token != "" {
				return token, nil
			}
		}
	}

	return "", ErrMissingCredentials
}

func (b *Broker) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")

	if origin == "" {
		// Native client (no Origin header sent by native mobile or Go CLI apps)
		// Native clients authenticate via supported headers validated in the handler prior to WebSocket upgrade.
		return true
	}

	// Browser request with Origin header: must match configured allowlist exactly (scheme, hostname, and port)
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ws" && scheme != "wss" {
		return false
	}

	normOrigin := scheme + "://" + strings.ToLower(u.Host)

	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.allowedOrigins[normOrigin] || b.allowedOrigins[origin]
}

func (b *Broker) AuthorizeHost(machineID, token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if machineID != "" && token != "" {
		b.authorizedHost[machineID] = token
	}
}

func (b *Broker) AuthorizeDevice(machineID, deviceID, sessionID, clientToken string) {
	b.AuthorizeDeviceWithExpiry(machineID, deviceID, sessionID, clientToken, 24*time.Hour)
}

func (b *Broker) AuthorizeDeviceWithExpiry(machineID, deviceID, sessionID, clientToken string, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if machineID != "" && deviceID != "" {
		b.authorizedDev[machineID+":"+deviceID] = true
		if sessionID != "" && clientToken != "" {
			key := machineID + ":" + deviceID + ":" + sessionID
			b.authorizedClientTokens[key] = clientToken
			if ttl > 0 {
				b.authorizedExpiries[key] = time.Now().Add(ttl)
			}
		}
	}
}

func (b *Broker) RevokeDevice(machineID, deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.authorizedDev, machineID+":"+deviceID)

	prefix := machineID + ":" + deviceID + ":"
	for key := range b.authorizedClientTokens {
		if strings.HasPrefix(key, prefix) {
			delete(b.authorizedClientTokens, key)
			delete(b.authorizedExpiries, key)
		}
	}

	for _, sess := range b.sessions {
		if sess.MachineID == machineID && sess.DeviceID == deviceID {
			b.closeSessionLocked(sess.ID)
		}
	}
}

func (b *Broker) RegisterHostConnection(machineID, sessionID, token string, conn *websocket.Conn) (*Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	expectedToken, registered := b.authorizedHost[machineID]
	if !registered || expectedToken == "" || token != expectedToken {
		return nil, ErrUnauthorizedSession
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

	expectedToken, tokenRegistered := b.authorizedClientTokens[machineID+":"+deviceID+":"+sessionID]
	if !tokenRegistered || expectedToken == "" || token != expectedToken {
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
	// 1. Explicitly reject credentials supplied through URL query parameters
	if r.URL.Query().Get("token") != "" || r.URL.Query().Has("token") ||
		r.URL.Query().Get("credential") != "" || r.URL.Query().Has("credential") ||
		r.URL.Query().Get("secret") != "" || r.URL.Query().Has("secret") {
		http.Error(w, "credentials in URL query parameters are strictly forbidden; use WebSocket authentication headers", http.StatusBadRequest)
		return
	}

	machineID := r.URL.Query().Get("machine_id")
	sessionID := r.URL.Query().Get("session_id")
	if machineID == "" || sessionID == "" {
		http.Error(w, "missing required machine_id or session_id query parameters", http.StatusBadRequest)
		return
	}

	// 2. Extract credential from supported headers
	token, err := extractToken(r)
	if err != nil {
		if errors.Is(err, ErrQueryCredentialsRejected) {
			http.Error(w, "credentials in URL query parameters are strictly forbidden", http.StatusBadRequest)
			return
		}
		http.Error(w, "missing required authentication credential in headers", http.StatusUnauthorized)
		return
	}

	// 3. Strict Origin Validation
	if !b.checkOrigin(r) {
		http.Error(w, ErrInvalidOrigin.Error(), http.StatusForbidden)
		return
	}

	// 4. Validate credentials server-side BEFORE completing WebSocket upgrade
	b.mu.RLock()
	expectedToken, registered := b.authorizedHost[machineID]
	b.mu.RUnlock()

	if !registered || expectedToken == "" || token != expectedToken {
		http.Error(w, "unauthorized host credential", http.StatusUnauthorized)
		return
	}

	// 5. Complete WebSocket upgrade only after credentials validated
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024 * 1024,
		WriteBufferSize: 1024 * 1024,
		CheckOrigin:     b.checkOrigin,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade host WebSocket: %v", err)
		return
	}
	conn.SetReadLimit(MaxFrameSizeBytes)

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
		_ = b.RelayFrameFromHostToClient(sessionID, &frame)
	}
}

func (b *Broker) HandleClientWebSocket(w http.ResponseWriter, r *http.Request) {
	// 1. Explicitly reject credentials supplied through URL query parameters
	if r.URL.Query().Get("token") != "" || r.URL.Query().Has("token") ||
		r.URL.Query().Get("credential") != "" || r.URL.Query().Has("credential") ||
		r.URL.Query().Get("secret") != "" || r.URL.Query().Has("secret") {
		http.Error(w, "credentials in URL query parameters are strictly forbidden; use WebSocket authentication headers", http.StatusBadRequest)
		return
	}

	machineID := r.URL.Query().Get("machine_id")
	deviceID := r.URL.Query().Get("device_id")
	sessionID := r.URL.Query().Get("session_id")
	if machineID == "" || deviceID == "" || sessionID == "" {
		http.Error(w, "missing required parameters: machine_id, device_id, or session_id", http.StatusBadRequest)
		return
	}

	// 2. Extract credential from supported headers
	token, err := extractToken(r)
	if err != nil {
		if errors.Is(err, ErrQueryCredentialsRejected) {
			http.Error(w, "credentials in URL query parameters are strictly forbidden", http.StatusBadRequest)
			return
		}
		http.Error(w, "missing required authentication credential in headers", http.StatusUnauthorized)
		return
	}

	// 3. Strict Origin Validation
	if !b.checkOrigin(r) {
		http.Error(w, ErrInvalidOrigin.Error(), http.StatusForbidden)
		return
	}

	// 4. Validate client device identity and token server-side BEFORE completing WebSocket upgrade
	b.mu.RLock()
	isAuthorized := b.authorizedDev[machineID+":"+deviceID]
	expectedToken, tokenRegistered := b.authorizedClientTokens[machineID+":"+deviceID+":"+sessionID]
	tokenExpiry, hasTokenExpiry := b.authorizedExpiries[machineID+":"+deviceID+":"+sessionID]
	sessExisting, sessExists := b.sessions[sessionID]
	b.mu.RUnlock()

	if !isAuthorized || !tokenRegistered || expectedToken == "" || token != expectedToken {
		http.Error(w, "unauthorized client device identity or invalid token credential", http.StatusForbidden)
		return
	}

	if hasTokenExpiry && time.Now().After(tokenExpiry) {
		http.Error(w, "session authorization token has expired", http.StatusUnauthorized)
		return
	}

	if sessExists && sessExisting != nil && time.Now().After(sessExisting.ExpiresAt) {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}

	// 5. Complete WebSocket upgrade only after credentials validated
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024 * 1024,
		WriteBufferSize: 1024 * 1024,
		CheckOrigin:     b.checkOrigin,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade client WebSocket: %v", err)
		return
	}
	conn.SetReadLimit(MaxFrameSizeBytes)

	sess, err := b.RegisterClientConnection(machineID, deviceID, sessionID, token, conn)
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}
	log.Printf("Broker: Authenticated Client WSS connected for machine=%s device=%s session=%s", machineID, deviceID, sessionID)

	// Explicit server-confirmed session acknowledgment frame
	ackFrame := FrameEnvelope{
		ProtocolVersion: "1.0",
		SessionID:       sessionID,
		Type:            "session_ready",
		Timestamp:       time.Now().UnixMicro(),
	}
	if err := conn.WriteJSON(ackFrame); err != nil {
		conn.Close()
		return
	}

	defer b.CloseSession(sess.ID)

	for {
		var frame FrameEnvelope
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
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

	if sess.ClientConn != nil {
		_ = sess.ClientConn.Close()
	}
	if sess.HostConn != nil {
		_ = sess.HostConn.Close()
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

func (b *Broker) verifyAdminAuth(r *http.Request) bool {
	b.mu.RLock()
	adminSecret := b.adminSecret
	b.mu.RUnlock()

	if adminSecret == "" {
		return false
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := strings.TrimSpace(parts[1])
			if subtle.ConstantTimeCompare([]byte(token), []byte(adminSecret)) == 1 {
				return true
			}
		}
	}

	if secretHeader := r.Header.Get("X-Broker-Secret"); secretHeader != "" {
		return subtle.ConstantTimeCompare([]byte(secretHeader), []byte(adminSecret)) == 1
	}

	return false
}

type AuthorizeSessionRequest struct {
	MachineID   string `json:"machine_id"`
	DeviceID    string `json:"device_id"`
	SessionID   string `json:"session_id"`
	ClientToken string `json:"client_token"`
	HostToken   string `json:"host_token,omitempty"`
	TTLSeconds  int64  `json:"ttl_seconds,omitempty"`
}

func (b *Broker) HandleAuthorizeSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !b.verifyAdminAuth(r) {
		http.Error(w, "Unauthorized: invalid service-to-service credentials", http.StatusUnauthorized)
		return
	}

	var req AuthorizeSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.MachineID == "" || req.DeviceID == "" || req.SessionID == "" || req.ClientToken == "" {
		http.Error(w, "missing required parameters: machine_id, device_id, session_id, and client_token are required", http.StatusBadRequest)
		return
	}

	ttl := 24 * time.Hour
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}

	b.AuthorizeDeviceWithExpiry(req.MachineID, req.DeviceID, req.SessionID, req.ClientToken, ttl)

	if req.HostToken != "" {
		b.AuthorizeHost(req.MachineID, req.HostToken)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "authorized",
		"session_id": req.SessionID,
		"machine_id": req.MachineID,
		"device_id":  req.DeviceID,
		"expires_at": time.Now().Add(ttl).Format(time.RFC3339),
	})
}

type RevokeRequest struct {
	MachineID string `json:"machine_id"`
	DeviceID  string `json:"device_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func (b *Broker) HandleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !b.verifyAdminAuth(r) {
		http.Error(w, "Unauthorized: invalid service-to-service credentials", http.StatusUnauthorized)
		return
	}

	var req RevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.MachineID == "" {
		http.Error(w, "machine_id is required", http.StatusBadRequest)
		return
	}

	if req.DeviceID != "" {
		b.RevokeDevice(req.MachineID, req.DeviceID)
	}

	if req.SessionID != "" {
		b.CloseSession(req.SessionID)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "revoked",
	})
}
