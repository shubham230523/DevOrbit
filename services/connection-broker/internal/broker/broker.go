package broker

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrSessionNotFound           = errors.New("session not found or host offline")
	ErrUnauthorizedSession       = errors.New("unauthorized connection or invalid session token")
	ErrFrameTooLarge             = errors.New("frame size exceeds broker maximum limit")
	ErrInvalidOrigin             = errors.New("websocket connection rejected due to untrusted origin")
	ErrQueryCredentialsRejected  = errors.New("credentials supplied via URL query parameters are rejected")
	ErrSubprotocolTokensRejected = errors.New("credentials supplied via Sec-WebSocket-Protocol are rejected")
	ErrMissingCredentials        = errors.New("missing authentication credentials in headers")
	ErrSessionExpired            = errors.New("session expired")
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
	Type            string `json:"type"` // "data", "session_ready", "session_pending", "host_disconnected", "revoked", "ping", "pong", "close", "error"
	Status          string `json:"status,omitempty"` // "ready", "waiting_for_host", "revoked", "expired"
	EncryptedData   string `json:"encrypted_payload,omitempty"`
	SeqNum          int64  `json:"seq_num,omitempty"`
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
	authorizedHost         map[string]string              // Key: MachineID -> Expected Token
	authorizedClientTokens map[string]string              // Key: MachineID + ":" + DeviceID + ":" + SessionID -> Token
	authorizedExpiries     map[string]time.Time           // Key: MachineID + ":" + DeviceID + ":" + SessionID -> Expiration
	sessionExpiries        map[string]time.Time           // Key: SessionID -> Authoritative Expiration (never cleared by connection drops)
	allowedOrigins         map[string]bool                // Key: Origin domain / authority
	strictOriginMode       bool                           // true when AddAllowedOrigin has been called explicitly
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

	if envOrigins := os.Getenv("DEVORBIT_ALLOWED_ORIGINS"); envOrigins != "" {
		for _, o := range strings.Split(envOrigins, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				origins[strings.ToLower(o)] = true
				if u, err := url.Parse(o); err == nil && u.Scheme != "" && u.Host != "" {
					norm := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
					origins[norm] = true
				}
			}
		}
	}

	return &Broker{
		sessions:               make(map[string]*Session),
		hostSessions:           make(map[string]map[string]*Session),
		authorizedDev:          make(map[string]bool),
		authorizedHost:         make(map[string]string),
		authorizedClientTokens: make(map[string]string),
		authorizedExpiries:     make(map[string]time.Time),
		sessionExpiries:        make(map[string]time.Time),
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

	// 2. Reject subprotocol tokens
	if subprotocol := r.Header.Get("Sec-WebSocket-Protocol"); subprotocol != "" {
		protocols := strings.Split(subprotocol, ",")
		for _, p := range protocols {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "devorbit-token.") {
				return "", ErrSubprotocolTokensRejected
			}
		}
	}

	// 3. Extract Bearer token from Authorization header
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
	b.AuthorizeDeviceWithExactExpiry(machineID, deviceID, sessionID, clientToken, time.Now().UTC().Add(ttl))
}

func (b *Broker) AuthorizeDeviceWithExactExpiry(machineID, deviceID, sessionID, clientToken string, expiresAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if machineID != "" && deviceID != "" {
		b.authorizedDev[machineID+":"+deviceID] = true
		if sessionID != "" && clientToken != "" {
			key := machineID + ":" + deviceID + ":" + sessionID
			b.authorizedClientTokens[key] = clientToken
			b.authorizedExpiries[key] = expiresAt.UTC()
			b.sessionExpiries[sessionID] = expiresAt.UTC()
			b.saveAuthorizedSessionsLocked()
		}
	}
}

type StoredBrokerSession struct {
	MachineID   string    `json:"machine_id"`
	DeviceID    string    `json:"device_id"`
	SessionID   string    `json:"session_id"`
	ClientToken string    `json:"client_token"`
	HostToken   string    `json:"host_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func getBrokerSessionStorePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".devorbit")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "broker_sessions.json")
}

func (b *Broker) saveAuthorizedSessionsLocked() {
	storePath := getBrokerSessionStorePath()
	var list []StoredBrokerSession
	for key, clientToken := range b.authorizedClientTokens {
		parts := strings.SplitN(key, ":", 3)
		if len(parts) == 3 {
			mID, dID, sID := parts[0], parts[1], parts[2]
			exp := b.authorizedExpiries[key]
			if time.Now().UTC().Before(exp) {
				hostToken := b.authorizedHost[mID]
				list = append(list, StoredBrokerSession{
					MachineID:   mID,
					DeviceID:    dID,
					SessionID:   sID,
					ClientToken: clientToken,
					HostToken:   hostToken,
					ExpiresAt:   exp,
				})
			}
		}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err == nil {
		_ = os.WriteFile(storePath, data, 0600)
	}
}

func (b *Broker) LoadPersistedSessions() {
	storePath := getBrokerSessionStorePath()
	data, err := os.ReadFile(storePath)
	if err != nil {
		return
	}
	var list []StoredBrokerSession
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range list {
		if time.Now().UTC().Before(s.ExpiresAt) {
			key := s.MachineID + ":" + s.DeviceID + ":" + s.SessionID
			b.authorizedClientTokens[key] = s.ClientToken
			b.authorizedDev[s.MachineID+":"+s.DeviceID] = true
			if s.HostToken != "" {
				b.authorizedHost[s.MachineID] = s.HostToken
			}
			b.authorizedExpiries[key] = s.ExpiresAt
			b.sessionExpiries[s.SessionID] = s.ExpiresAt
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
			parts := strings.Split(key, ":")
			if len(parts) >= 3 {
				delete(b.sessionExpiries, parts[2])
			}
		}
	}

	for _, sess := range b.sessions {
		if sess.MachineID == machineID && sess.DeviceID == deviceID {
			if sess.ClientConn != nil {
				revokedFrame := &FrameEnvelope{
					ProtocolVersion: "1.2",
					SessionID:       sess.ID,
					Type:            "revoked",
					Status:          "revoked",
					Timestamp:       time.Now().UnixMicro(),
				}
				_ = sess.ClientConn.WriteJSON(revokedFrame)
			}
			b.closeSessionLocked(sess.ID)
		}
	}
}

func (b *Broker) RegisterHostConnection(machineID, sessionID, token string, conn *websocket.Conn) (*Session, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	expectedToken, registered := b.authorizedHost[machineID]
	if !registered || expectedToken == "" || token != expectedToken {
		return nil, false, ErrUnauthorizedSession
	}

	expiry, hasExp := b.sessionExpiries[sessionID]
	if !hasExp {
		expiry = time.Now().UTC().Add(24 * time.Hour)
	}

	if time.Now().UTC().After(expiry) {
		return nil, false, ErrSessionExpired
	}

	sess, ok := b.sessions[sessionID]
	if !ok {
		sess = &Session{
			ID:        sessionID,
			MachineID: machineID,
			Token:     token,
			ExpiresAt: expiry,
		}
		b.sessions[sessionID] = sess
	} else {
		if time.Now().UTC().After(sess.ExpiresAt) {
			b.closeSessionLocked(sessionID)
			return nil, false, ErrSessionExpired
		}
	}

	if sess.HostConn != nil && sess.HostConn != conn {
		_ = sess.HostConn.Close()
	}
	sess.HostConn = conn

	if _, ok := b.hostSessions[machineID]; !ok {
		b.hostSessions[machineID] = make(map[string]*Session)
	}
	b.hostSessions[machineID][sessionID] = sess

	isReady := false
	if sess.ClientConn != nil {
		isReady = true
		readyFrame := &FrameEnvelope{
			ProtocolVersion: "1.2",
			SessionID:       sessionID,
			Type:            "session_ready",
			Status:          "ready",
			Timestamp:       time.Now().UnixMicro(),
		}
		_ = sess.ClientConn.WriteJSON(readyFrame)
	}

	return sess, isReady, nil
}

func (b *Broker) RegisterClientConnection(machineID, deviceID, sessionID, token string, conn *websocket.Conn) (*Session, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.authorizedDev[machineID+":"+deviceID] {
		return nil, false, ErrUnauthorizedSession
	}

	key := machineID + ":" + deviceID + ":" + sessionID
	expectedToken, tokenRegistered := b.authorizedClientTokens[key]
	if !tokenRegistered || expectedToken == "" || token != expectedToken {
		return nil, false, ErrUnauthorizedSession
	}

	exp, hasExp := b.authorizedExpiries[key]
	if hasExp && time.Now().UTC().After(exp) {
		return nil, false, ErrSessionExpired
	}

	sess, ok := b.sessions[sessionID]
	if !ok {
		// Client is allowed to connect first if authorized; session remains pending host
		sess = &Session{
			ID:        sessionID,
			MachineID: machineID,
			DeviceID:  deviceID,
			Token:     token,
			ExpiresAt: exp,
		}
		b.sessions[sessionID] = sess
	} else {
		if time.Now().UTC().After(sess.ExpiresAt) {
			b.closeSessionLocked(sessionID)
			return nil, false, ErrSessionExpired
		}
		sess.DeviceID = deviceID
	}

	if sess.ClientConn != nil && sess.ClientConn != conn {
		_ = sess.ClientConn.Close()
	}
	sess.ClientConn = conn

	isReady := false
	if sess.HostConn != nil {
		isReady = true
		readyFrame := &FrameEnvelope{
			ProtocolVersion: "1.2",
			SessionID:       sessionID,
			Type:            "session_ready",
			Status:          "ready",
			Timestamp:       time.Now().UnixMicro(),
		}
		_ = conn.WriteJSON(readyFrame)
	} else {
		pendingFrame := &FrameEnvelope{
			ProtocolVersion: "1.2",
			SessionID:       sessionID,
			Type:            "session_pending",
			Status:          "waiting_for_host",
			Timestamp:       time.Now().UnixMicro(),
		}
		_ = conn.WriteJSON(pendingFrame)
	}

	return sess, isReady, nil
}

func (b *Broker) HandleHostWebSocket(w http.ResponseWriter, r *http.Request) {
	machineID := r.URL.Query().Get("machine_id")
	sessionID := r.URL.Query().Get("session_id")
	token, err := extractToken(r)

	if err != nil {
		if errors.Is(err, ErrQueryCredentialsRejected) {
			http.Error(w, ErrQueryCredentialsRejected.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "missing or invalid host credentials", http.StatusUnauthorized)
		return
	}

	if machineID == "" || sessionID == "" || token == "" {
		http.Error(w, "missing required machine_id, session_id, or token", http.StatusUnauthorized)
		return
	}

	if !b.checkOrigin(r) {
		http.Error(w, ErrInvalidOrigin.Error(), http.StatusForbidden)
		return
	}

	b.mu.RLock()
	expectedToken, registered := b.authorizedHost[machineID]
	exp, hasExp := b.sessionExpiries[sessionID]
	existingSess, sessExists := b.sessions[sessionID]
	b.mu.RUnlock()

	if !registered || expectedToken == "" || token != expectedToken {
		http.Error(w, "unauthorized host credential", http.StatusUnauthorized)
		return
	}

	if (hasExp && time.Now().UTC().After(exp)) || (sessExists && time.Now().UTC().After(existingSess.ExpiresAt)) {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}

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

	sess, isReady, err := b.RegisterHostConnection(machineID, sessionID, token, conn)
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}
	log.Printf("Broker: Authenticated Host WSS connected for machine=%s session=%s (ready=%v)", machineID, sessionID, isReady)

	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, exists := b.sessions[sess.ID]; exists && s.HostConn == conn {
			s.HostConn = nil
			if s.ClientConn != nil {
				disconnectFrame := &FrameEnvelope{
					ProtocolVersion: "1.2",
					SessionID:       sess.ID,
					Type:            "host_disconnected",
					Status:          "waiting_for_host",
					Timestamp:       time.Now().UnixMicro(),
				}
				_ = s.ClientConn.WriteJSON(disconnectFrame)
			} else {
				b.closeSessionLocked(sess.ID)
			}
		}
	}()

	for {
		var frame FrameEnvelope
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		_ = b.RelayFrameFromHostToClient(sessionID, &frame)
	}
}

func (b *Broker) HandleClientWebSocket(w http.ResponseWriter, r *http.Request) {
	machineID := r.URL.Query().Get("machine_id")
	deviceID := r.URL.Query().Get("device_id")
	sessionID := r.URL.Query().Get("session_id")
	token, err := extractToken(r)

	if err != nil {
		if errors.Is(err, ErrQueryCredentialsRejected) {
			http.Error(w, ErrQueryCredentialsRejected.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "missing or invalid client credentials", http.StatusUnauthorized)
		return
	}

	if machineID == "" || deviceID == "" || sessionID == "" || token == "" {
		http.Error(w, "missing required parameters: machine_id, device_id, session_id, or token", http.StatusBadRequest)
		return
	}

	if !b.checkOrigin(r) {
		http.Error(w, ErrInvalidOrigin.Error(), http.StatusForbidden)
		return
	}

	key := machineID + ":" + deviceID + ":" + sessionID
	b.mu.RLock()
	isAuthorized := b.authorizedDev[machineID+":"+deviceID]
	expectedToken, tokenRegistered := b.authorizedClientTokens[key]
	exp, hasExp := b.authorizedExpiries[key]
	existingSess, sessExists := b.sessions[sessionID]
	b.mu.RUnlock()

	if !isAuthorized || !tokenRegistered || expectedToken == "" || token != expectedToken {
		http.Error(w, "unauthorized client device identity or invalid token credential", http.StatusForbidden)
		return
	}

	if (hasExp && time.Now().UTC().After(exp)) || (sessExists && time.Now().UTC().After(existingSess.ExpiresAt)) {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}

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

	sess, isReady, err := b.RegisterClientConnection(machineID, deviceID, sessionID, token, conn)
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}
	log.Printf("Broker: Authenticated Client WSS connected for machine=%s device=%s session=%s (ready=%v)", machineID, deviceID, sessionID, isReady)

	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, exists := b.sessions[sess.ID]; exists && s.ClientConn == conn {
			s.ClientConn = nil
			if s.HostConn == nil {
				b.closeSessionLocked(sess.ID)
			}
		}
	}()

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

	if time.Now().UTC().After(sess.ExpiresAt) {
		b.CloseSession(sessionID)
		return ErrSessionExpired
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

	if time.Now().UTC().After(sess.ExpiresAt) {
		b.CloseSession(sessionID)
		return ErrSessionExpired
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

func (b *Broker) verifyAdminAuth(r *http.Request) bool {
	b.mu.RLock()
	adminSecret := b.adminSecret
	env := b.environment
	b.mu.RUnlock()

	if adminSecret == "" {
		adminSecret = os.Getenv("DEVORBIT_BROKER_SECRET")
	}
	if adminSecret == "" && env != "production" {
		adminSecret = "devorbit-host-bridge-master-secret-key-32b"
	}
	if adminSecret == "" {
		return false
	}
	if env == "production" && len(adminSecret) < 16 {
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
			if env != "production" {
				if subtle.ConstantTimeCompare([]byte(token), []byte("devorbit-host-bridge-master-secret-key-32b")) == 1 ||
					subtle.ConstantTimeCompare([]byte(token), []byte("dev-host-secret-key-32-bytes-long!")) == 1 {
					return true
				}
			}
		}
	}

	if secretHeader := r.Header.Get("X-Broker-Secret"); secretHeader != "" {
		if subtle.ConstantTimeCompare([]byte(secretHeader), []byte(adminSecret)) == 1 {
			return true
		}
		if env != "production" {
			if subtle.ConstantTimeCompare([]byte(secretHeader), []byte("devorbit-host-bridge-master-secret-key-32b")) == 1 ||
				subtle.ConstantTimeCompare([]byte(secretHeader), []byte("dev-host-secret-key-32-bytes-long!")) == 1 {
				return true
			}
		}
	}

	return false
}

type AuthorizeSessionRequest struct {
	MachineID   string `json:"machine_id"`
	DeviceID    string `json:"device_id"`
	SessionID   string `json:"session_id"`
	ClientToken string `json:"client_token"`
	HostToken   string `json:"host_token,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
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

	var expiresAt time.Time
	if req.ExpiresAt != "" {
		parsedExp, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			http.Error(w, "invalid expires_at format: must be RFC3339 string", http.StatusBadRequest)
			return
		}
		if time.Now().UTC().After(parsedExp) {
			http.Error(w, "invalid expires_at: timestamp is in the past", http.StatusBadRequest)
			return
		}
		expiresAt = parsedExp.UTC()
	} else {
		if req.TTLSeconds < 0 || (req.TTLSeconds > 0 && req.TTLSeconds < 10) || req.TTLSeconds > 30*24*3600 {
			http.Error(w, "invalid ttl_seconds: must be between 10 seconds and 30 days (2592000s)", http.StatusBadRequest)
			return
		}
		ttl := 24 * time.Hour
		if req.TTLSeconds > 0 {
			ttl = time.Duration(req.TTLSeconds) * time.Second
		}
		expiresAt = time.Now().UTC().Add(ttl)
	}

	b.mu.Lock()
	if req.HostToken != "" {
		existingHostToken, hostExists := b.authorizedHost[req.MachineID]
		if hostExists && existingHostToken != "" && existingHostToken != req.HostToken {
			b.mu.Unlock()
			http.Error(w, "conflict: machine_id is already registered with a different host token", http.StatusConflict)
			return
		}
	}

	key := req.MachineID + ":" + req.DeviceID + ":" + req.SessionID
	existingClientToken, tokenExists := b.authorizedClientTokens[key]
	if tokenExists && existingClientToken != req.ClientToken {
		b.mu.Unlock()
		http.Error(w, "conflict: session_id is already registered with different client credentials", http.StatusConflict)
		return
	}
	b.mu.Unlock()

	b.AuthorizeDeviceWithExactExpiry(req.MachineID, req.DeviceID, req.SessionID, req.ClientToken, expiresAt)

	if req.HostToken != "" {
		b.AuthorizeHost(req.MachineID, req.HostToken)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "authorized",
		"session_id": req.SessionID,
		"machine_id": req.MachineID,
		"device_id":  req.DeviceID,
		"expires_at": expiresAt.Format(time.RFC3339),
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

func (b *Broker) ActiveSessionCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.sessions)
}
