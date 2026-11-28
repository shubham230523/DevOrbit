package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"devorbit.net/agents/host-bridge/internal/security"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
	"github.com/gorilla/websocket"
)

type FrameEnvelope struct {
	ProtocolVersion string `json:"v"`
	SessionID       string `json:"session_id"`
	Type            string `json:"type"` // "data", "ping", "pong", "close"
	EncryptedData   string `json:"encrypted_payload"`
	SeqNum          int64  `json:"seq_num"`
	Timestamp       int64  `json:"timestamp"`
}

type ApplicationRequestEnvelope struct {
	ProtocolVersion string            `json:"v"`
	SessionID       string            `json:"session_id"`
	RequestID       string            `json:"request_id"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	SeqNum          int64             `json:"seq_num"`
	Timestamp       int64             `json:"timestamp"`
}

type ApplicationResponseEnvelope struct {
	ProtocolVersion string            `json:"v"`
	SessionID       string            `json:"session_id"`
	RequestID       string            `json:"request_id"`
	StatusCode      int               `json:"status_code"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	SeqNum          int64             `json:"seq_num"`
	Timestamp       int64             `json:"timestamp"`
}

type DeviceSession struct {
	SessionID    string
	DeviceID     string
	MasterSecret string
	SecretRef    string
	E2ESession   *security.E2ESession
	ExpiresAt    time.Time
	cancel       context.CancelFunc
	mu           sync.Mutex
	conn         *websocket.Conn
}

type BrokerClient struct {
	mu              sync.RWMutex
	BrokerURL       string
	MachineID       string
	SessionID       string // Legacy/single session convenience
	HostToken       string
	Server          *httptransport.Server
	E2ESession      *security.E2ESession
	sessions        map[string]*DeviceSession  // Key: SessionID -> DeviceSession
	deviceSessions  map[string]map[string]bool // Key: DeviceID -> set of SessionIDs
	revokedSessions map[string]time.Time
	revokedDevices  map[string]time.Time
	stopChan        chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	storePath       string
	secretStore     security.SecretStore
	persistSecrets  bool
}

func NewBrokerClient(brokerURL, machineID, sessionID, hostToken string, server *httptransport.Server) *BrokerClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &BrokerClient{
		BrokerURL:       brokerURL,
		MachineID:       machineID,
		SessionID:       sessionID,
		HostToken:       hostToken,
		Server:          server,
		sessions:        make(map[string]*DeviceSession),
		deviceSessions:  make(map[string]map[string]bool),
		revokedSessions: make(map[string]time.Time),
		revokedDevices:  make(map[string]time.Time),
		stopChan:        make(chan struct{}),
		ctx:             ctx,
		cancel:          cancel,
		secretStore:     security.NewDefaultSecretStore(),
	}
}

func (bc *BrokerClient) SetSecretStore(ss security.SecretStore) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.secretStore = ss
	if bc.storePath != "" {
		bc.persistSecrets = ss != nil && ss.Available()
	}
}

func (bc *BrokerClient) SetE2ESession(sess *security.E2ESession) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if sess != nil {
		sessCtx, sessCancel := context.WithCancel(bc.ctx)
		ds := &DeviceSession{
			SessionID:  sess.SessionID,
			DeviceID:   sess.DeviceID,
			E2ESession: sess,
			ExpiresAt:  sess.ExpiresAt,
			cancel:     sessCancel,
		}
		bc.sessions[sess.SessionID] = ds
		if _, ok := bc.deviceSessions[sess.DeviceID]; !ok {
			bc.deviceSessions[sess.DeviceID] = make(map[string]bool)
		}
		bc.deviceSessions[sess.DeviceID][sess.SessionID] = true
		bc.SessionID = sess.SessionID
		go bc.runSessionLoop(sessCtx, ds)
	}
}

func (bc *BrokerClient) ActiveSession() (string, *security.E2ESession) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	if bc.SessionID != "" {
		if ds, ok := bc.sessions[bc.SessionID]; ok {
			return bc.SessionID, ds.E2ESession
		}
	}
	for id, ds := range bc.sessions {
		return id, ds.E2ESession
	}
	return "", nil
}

func (bc *BrokerClient) ActiveSessionCount() int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return len(bc.sessions)
}

func (bc *BrokerClient) ProvisionSession(sessionID, deviceID, sessionSecret string, expiryArg any) error {
	return bc.provisionSessionInternal(sessionID, deviceID, sessionSecret, "", expiryArg, true)
}

func (bc *BrokerClient) provisionSessionInternal(sessionID, deviceID, sessionSecret, existingRef string, expiryArg any, persistNow bool) error {
	if sessionID == "" || deviceID == "" || sessionSecret == "" {
		return errors.New("provisioning failed: sessionID, deviceID, and sessionSecret are required")
	}
	if sessionSecret == bc.HostToken {
		return errors.New("security error: per-device session secret cannot be identical to host-wide secret")
	}

	var expiry time.Time
	switch v := expiryArg.(type) {
	case time.Time:
		expiry = v.UTC()
	case time.Duration:
		expiry = time.Now().UTC().Add(v)
	default:
		return errors.New("provisioning failed: invalid expiry type, must be time.Time or time.Duration")
	}

	ttl := time.Until(expiry)
	if ttl <= 0 {
		return errors.New("provisioning failed: session expiry is in the past")
	}

	e2eSess, err := security.NewE2ESession(sessionID, deviceID, sessionSecret, ttl)
	if err != nil {
		return fmt.Errorf("failed to create provisioned E2E session: %w", err)
	}

	bc.mu.Lock()
	if _, revokedSess := bc.revokedSessions[sessionID]; revokedSess {
		bc.mu.Unlock()
		return security.ErrSessionRevoked
	}
	delete(bc.revokedDevices, deviceID)

	// Clean up existing session with the same sessionID if any
	if existing, ok := bc.sessions[sessionID]; ok {
		existing.cancel()
		existing.mu.Lock()
		if existing.conn != nil {
			_ = existing.conn.Close()
		}
		existing.mu.Unlock()
	}

	sessCtx, sessCancel := context.WithCancel(bc.ctx)
	ds := &DeviceSession{
		SessionID:    sessionID,
		DeviceID:     deviceID,
		MasterSecret: sessionSecret,
		SecretRef:    existingRef,
		E2ESession:   e2eSess,
		ExpiresAt:    expiry,
		cancel:       sessCancel,
	}

	bc.sessions[sessionID] = ds
	if _, ok := bc.deviceSessions[deviceID]; !ok {
		bc.deviceSessions[deviceID] = make(map[string]bool)
	}
	bc.deviceSessions[deviceID][sessionID] = true
	bc.SessionID = sessionID // Update primary session pointer
	bc.E2ESession = e2eSess

	var persistErr error
	if persistNow && bc.storePath != "" {
		storedNew := false
		if bc.persistSecrets {
			if bc.secretStore == nil || !bc.secretStore.Available() {
				persistErr = fmt.Errorf("%w: %w", security.ErrPersistenceFailed, security.ErrSecureStoreUnavailable)
			} else {
				ref, storeErr := bc.secretStore.Store("broker_session", sessionID, []byte(sessionSecret))
				if storeErr != nil {
					persistErr = fmt.Errorf("%w: %w", security.ErrPersistenceFailed, storeErr)
				} else {
					ds.SecretRef = ref
					storedNew = true
				}
			}
		}
		if persistErr == nil {
			if saveErr := bc.saveSessionsToDiskLocked(); saveErr != nil {
				if storedNew && bc.secretStore != nil {
					_ = bc.secretStore.Delete("broker_session", sessionID, ds.SecretRef)
					ds.SecretRef = ""
				}
				persistErr = saveErr
			}
		}
	}
	bc.mu.Unlock()

	// Launch dedicated, concurrent session loop for this device session
	go bc.runSessionLoop(sessCtx, ds)

	log.Printf("BrokerClient: Authenticated independent session provisioned for session=%s device=%s (total active: %d)",
		sessionID, deviceID, bc.ActiveSessionCount())
	return persistErr
}

const persistedBrokerStoreVersion = 2

type StoredSession struct {
	SessionID string    `json:"session_id"`
	DeviceID  string    `json:"device_id"`
	SecretRef string    `json:"secret_ref,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

type persistedBrokerStoreFile struct {
	Version         int                  `json:"version"`
	Sessions        []StoredSession      `json:"sessions"`
	RevokedSessions map[string]time.Time `json:"revoked_sessions,omitempty"`
	RevokedDevices  map[string]time.Time `json:"revoked_devices,omitempty"`
}

func (bc *BrokerClient) EnableSessionPersistence(customPath string) error {
	path := customPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		path = filepath.Join(home, ".devorbit", "host_sessions.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("%w: %w", security.ErrPersistenceFailed, err)
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.storePath = path
	if bc.secretStore == nil || !bc.secretStore.Available() {
		bc.persistSecrets = false
		return security.ErrSecureStoreUnavailable
	}
	bc.persistSecrets = true
	return nil
}

func (bc *BrokerClient) saveSessionsToDiskLocked() error {
	if bc.storePath == "" {
		return nil
	}
	now := time.Now().UTC()
	list := make([]StoredSession, 0)
	if bc.persistSecrets {
		for _, ds := range bc.sessions {
			if ds.SecretRef == "" || !now.Before(ds.ExpiresAt) {
				continue
			}
			if _, revSess := bc.revokedSessions[ds.SessionID]; revSess {
				continue
			}
			if _, revDev := bc.revokedDevices[ds.DeviceID]; revDev {
				continue
			}
			list = append(list, StoredSession{
				SessionID: ds.SessionID,
				DeviceID:  ds.DeviceID,
				SecretRef: ds.SecretRef,
				ExpiresAt: ds.ExpiresAt,
			})
		}
	}

	revSessCopy := make(map[string]time.Time, len(bc.revokedSessions))
	for k, v := range bc.revokedSessions {
		revSessCopy[k] = v
	}
	revDevCopy := make(map[string]time.Time, len(bc.revokedDevices))
	for k, v := range bc.revokedDevices {
		revDevCopy[k] = v
	}

	filePayload := persistedBrokerStoreFile{
		Version:         persistedBrokerStoreVersion,
		Sessions:        list,
		RevokedSessions: revSessCopy,
		RevokedDevices:  revDevCopy,
	}
	data, err := json.MarshalIndent(filePayload, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %w", security.ErrPersistenceFailed, err)
	}
	if err := security.WriteFileAtomic(bc.storePath, data, 0600); err != nil {
		return fmt.Errorf("%w: %w", security.ErrPersistenceFailed, err)
	}
	return nil
}

func (bc *BrokerClient) LoadPersistedSessions() error {
	bc.mu.RLock()
	storePath := bc.storePath
	ss := bc.secretStore
	bc.mu.RUnlock()

	if storePath == "" {
		return nil
	}
	data, err := os.ReadFile(storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: %w", security.ErrPersistenceFailed, err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty broker session file", security.ErrCorruptPersistedState)
	}
	if trimmed[0] == '[' {
		return fmt.Errorf("%w: legacy plaintext broker session file rejected; re-pairing required", security.ErrCorruptPersistedState)
	}

	var stored persistedBrokerStoreFile
	if err := json.Unmarshal(trimmed, &stored); err != nil {
		return fmt.Errorf("%w: %w", security.ErrCorruptPersistedState, err)
	}
	if stored.Version != persistedBrokerStoreVersion {
		return fmt.Errorf("%w: unsupported broker session version %d", security.ErrCorruptPersistedState, stored.Version)
	}

	bc.mu.Lock()
	for k, v := range stored.RevokedSessions {
		bc.revokedSessions[k] = v
	}
	for k, v := range stored.RevokedDevices {
		bc.revokedDevices[k] = v
	}
	bc.mu.Unlock()

	now := time.Now().UTC()
	var loadErrs []error
	for _, s := range stored.Sessions {
		if !now.Before(s.ExpiresAt) || s.SessionID == "" || s.DeviceID == "" || s.SecretRef == "" {
			continue
		}
		bc.mu.RLock()
		_, revSess := bc.revokedSessions[s.SessionID]
		_, revDev := bc.revokedDevices[s.DeviceID]
		bc.mu.RUnlock()
		if revSess || revDev {
			continue
		}
		if ss == nil || !ss.Available() {
			loadErrs = append(loadErrs, fmt.Errorf("%w: cannot unseal broker session %q", security.ErrSecureStoreUnavailable, s.SessionID))
			continue
		}
		secretBytes, err := ss.Load("broker_session", s.SessionID, s.SecretRef)
		if err != nil || len(secretBytes) == 0 {
			loadErrs = append(loadErrs, fmt.Errorf("%w: failed loading broker secret %q: %v", security.ErrCorruptPersistedState, s.SessionID, err))
			continue
		}
		secretStr := string(secretBytes)
		for i := range secretBytes {
			secretBytes[i] = 0
		}
		if err := bc.provisionSessionInternal(s.SessionID, s.DeviceID, secretStr, s.SecretRef, s.ExpiresAt, false); err != nil {
			loadErrs = append(loadErrs, err)
		}
	}
	return errors.Join(loadErrs...)
}

func (bc *BrokerClient) RevokeSession(sessionID string) {
	now := time.Now().UTC()
	bc.mu.Lock()
	bc.revokedSessions[sessionID] = now

	ds, ok := bc.sessions[sessionID]
	if ok {
		ds.cancel()
		ds.mu.Lock()
		if ds.conn != nil {
			_ = ds.conn.Close()
			ds.conn = nil
		}
		ds.mu.Unlock()

		if bc.persistSecrets && bc.secretStore != nil && ds.SecretRef != "" {
			if err := bc.secretStore.Delete("broker_session", sessionID, ds.SecretRef); err != nil {
				log.Printf("BrokerClient Warning: failed removing secret for revoked session %s: %v", sessionID, err)
			}
		}
		ds.MasterSecret = ""
		ds.SecretRef = ""

		delete(bc.sessions, sessionID)
		if m, ok := bc.deviceSessions[ds.DeviceID]; ok {
			delete(m, sessionID)
			if len(m) == 0 {
				delete(bc.deviceSessions, ds.DeviceID)
			}
		}
	}

	if bc.SessionID == sessionID {
		bc.SessionID = ""
		bc.E2ESession = nil
		for remaining, remDS := range bc.sessions {
			bc.SessionID = remaining
			bc.E2ESession = remDS.E2ESession
			break
		}
	}
	if err := bc.saveSessionsToDiskLocked(); err != nil {
		log.Printf("BrokerClient Warning: failed persisting session revocation for %s: %v", sessionID, err)
	}
	bc.mu.Unlock()

	if bc.Server != nil {
		if _, err := bc.Server.SessionStore().RevokeSession(sessionID); err != nil {
			log.Printf("BrokerClient Warning: failed persisting Host Bridge revocation for session %s: %v", sessionID, err)
		}
	}
	log.Printf("BrokerClient: Session %s revoked and disconnected (remaining active: %d)", sessionID, bc.ActiveSessionCount())
}

func (bc *BrokerClient) RevokeDevice(deviceID string) {
	now := time.Now().UTC()
	bc.mu.Lock()
	bc.revokedDevices[deviceID] = now

	if sessionIDs, ok := bc.deviceSessions[deviceID]; ok {
		for sID := range sessionIDs {
			bc.revokedSessions[sID] = now
			if ds, exists := bc.sessions[sID]; exists {
				ds.cancel()
				ds.mu.Lock()
				if ds.conn != nil {
					_ = ds.conn.Close()
					ds.conn = nil
				}
				ds.mu.Unlock()
				if bc.persistSecrets && bc.secretStore != nil && ds.SecretRef != "" {
					if err := bc.secretStore.Delete("broker_session", sID, ds.SecretRef); err != nil {
						log.Printf("BrokerClient Warning: failed deleting secret for device session %s: %v", sID, err)
					}
				}
				ds.MasterSecret = ""
				ds.SecretRef = ""
				delete(bc.sessions, sID)
			}
		}
		delete(bc.deviceSessions, deviceID)
	}

	if _, exists := bc.sessions[bc.SessionID]; !exists {
		bc.SessionID = ""
		bc.E2ESession = nil
		for remaining, remDS := range bc.sessions {
			bc.SessionID = remaining
			bc.E2ESession = remDS.E2ESession
			break
		}
	}
	if err := bc.saveSessionsToDiskLocked(); err != nil {
		log.Printf("BrokerClient Warning: failed persisting device revocation for %s: %v", deviceID, err)
	}
	bc.mu.Unlock()

	if bc.Server != nil {
		if _, err := bc.Server.SessionStore().RevokeDevice(deviceID); err != nil {
			log.Printf("BrokerClient Warning: failed persisting Host Bridge device revocation for %s: %v", deviceID, err)
		}
	}
	log.Printf("BrokerClient: Device %s and all its sessions revoked (remaining active: %d)", deviceID, bc.ActiveSessionCount())
}

func (bc *BrokerClient) runSessionLoop(ctx context.Context, ds *DeviceSession) {
	backoff := 1 * time.Second
	maxBackoff := 10 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if time.Now().UTC().After(ds.ExpiresAt) {
			log.Printf("HostBridge BrokerClient: Session %s expired. Terminating session worker.", ds.SessionID)
			bc.RevokeSession(ds.SessionID)
			return
		}

		wsURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", bc.BrokerURL, bc.MachineID, ds.SessionID)
		headers := http.Header{
			"Authorization": []string{"Bearer " + bc.HostToken},
		}

		dialer := websocket.DefaultDialer
		dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
		conn, res, err := dialer.DialContext(dialCtx, wsURL, headers)
		dialCancel()

		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if res != nil && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) {
				log.Printf("HostBridge BrokerClient: Connection rejected by broker with HTTP %d for session %s (revoked or expired). Disconnecting.", res.StatusCode, ds.SessionID)
				bc.RevokeSession(ds.SessionID)
				return
			}
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		ds.mu.Lock()
		ds.conn = conn
		ds.mu.Unlock()
		backoff = 1 * time.Second
		log.Printf("HostBridge BrokerClient: Connected to broker WSS for session=%s device=%s", ds.SessionID, ds.DeviceID)

		for {
			var frame FrameEnvelope
			if err := conn.ReadJSON(&frame); err != nil {
				ds.mu.Lock()
				if ds.conn == conn {
					ds.conn = nil
				}
				ds.mu.Unlock()
				_ = conn.Close()
				break
			}

			if frame.Type == "ping" {
				ds.mu.Lock()
				_ = conn.WriteJSON(FrameEnvelope{
					ProtocolVersion: "1.2",
					SessionID:       ds.SessionID,
					Type:            "pong",
					Timestamp:       time.Now().UnixNano(),
				})
				ds.mu.Unlock()
				continue
			}

			if frame.Type == "data" && ds.E2ESession != nil && bc.Server != nil {
				// Process request concurrently per session worker
				go func(f FrameEnvelope) {
					plainReqBytes, err := ds.E2ESession.Decrypt(security.DirClientToHost, f.EncryptedData, f.SeqNum, f.Timestamp)
					if err != nil {
						log.Printf("HostBridge BrokerClient: Decryption failed for session %s seq=%d: %v", ds.SessionID, f.SeqNum, err)
						return
					}

					var appReq ApplicationRequestEnvelope
					if err := json.Unmarshal(plainReqBytes, &appReq); err != nil {
						log.Printf("HostBridge BrokerClient: Failed to unmarshal ApplicationRequestEnvelope: %v", err)
						return
					}

					if appReq.SessionID != "" && appReq.SessionID != ds.SessionID {
						respTS := time.Now().UnixNano()
						errResp := ApplicationResponseEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       ds.SessionID,
							RequestID:       appReq.RequestID,
							StatusCode:      http.StatusForbidden,
							Body:            `{"error":"session_id mismatch in broker request envelope"}`,
							SeqNum:          f.SeqNum,
							Timestamp:       respTS,
						}
						respBytes, _ := json.Marshal(errResp)
						encryptedRespPayload, _ := ds.E2ESession.Encrypt(security.DirHostToClient, respBytes, f.SeqNum, respTS)
						ds.mu.Lock()
						if ds.conn != nil {
							_ = ds.conn.WriteJSON(FrameEnvelope{
								ProtocolVersion: "1.2",
								SessionID:       ds.SessionID,
								Type:            "data",
								EncryptedData:   encryptedRespPayload,
								SeqNum:          f.SeqNum,
								Timestamp:       respTS,
							})
						}
						ds.mu.Unlock()
						return
					}

					if appReq.Method == "" || appReq.Path == "" {
						respTS := time.Now().UnixNano()
						errResp := ApplicationResponseEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       ds.SessionID,
							RequestID:       appReq.RequestID,
							StatusCode:      http.StatusBadRequest,
							Body:            `{"error":"invalid request envelope: method and path are required"}`,
							SeqNum:          f.SeqNum,
							Timestamp:       respTS,
						}
						respBytes, _ := json.Marshal(errResp)
						encryptedRespPayload, _ := ds.E2ESession.Encrypt(security.DirHostToClient, respBytes, f.SeqNum, respTS)
						ds.mu.Lock()
						if ds.conn != nil {
							_ = ds.conn.WriteJSON(FrameEnvelope{
								ProtocolVersion: "1.2",
								SessionID:       ds.SessionID,
								Type:            "data",
								EncryptedData:   encryptedRespPayload,
								SeqNum:          f.SeqNum,
								Timestamp:       respTS,
							})
						}
						ds.mu.Unlock()
						return
					}

					req, err := http.NewRequest(appReq.Method, appReq.Path, strings.NewReader(appReq.Body))
					if err != nil {
						log.Printf("HostBridge BrokerClient: Invalid request (%s %s): %v", appReq.Method, appReq.Path, err)
						return
					}

					rec := NewResponseWriterBuffer()
					bc.Server.ServeBrokerRequest(rec, req, ds.SessionID, ds.DeviceID, appReq.Headers)

					respTS := time.Now().UnixNano()
					appResp := ApplicationResponseEnvelope{
						ProtocolVersion: "1.2",
						SessionID:       ds.SessionID,
						RequestID:       appReq.RequestID,
						StatusCode:      rec.Code,
						Body:            rec.Body.String(),
						SeqNum:          f.SeqNum,
						Timestamp:       respTS,
					}

					respBytes, err := json.Marshal(appResp)
					if err != nil {
						return
					}

					encryptedRespPayload, err := ds.E2ESession.Encrypt(security.DirHostToClient, respBytes, f.SeqNum, respTS)
					if err == nil {
						ds.mu.Lock()
						if ds.conn != nil {
							_ = ds.conn.WriteJSON(FrameEnvelope{
								ProtocolVersion: "1.2",
								SessionID:       ds.SessionID,
								Type:            "data",
								EncryptedData:   encryptedRespPayload,
								SeqNum:          f.SeqNum,
								Timestamp:       respTS,
							})
						}
						ds.mu.Unlock()
					}
				}(frame)
			}
		}
	}
}

func (bc *BrokerClient) Start(ctx context.Context) {
	if err := bc.LoadPersistedSessions(); err != nil {
		log.Printf("BrokerClient Warning: failed loading persisted sessions (fail-closed; re-pairing may be required): %v", err)
	}
}

func (bc *BrokerClient) Stop() {
	bc.cancel()
	close(bc.stopChan)
	bc.mu.Lock()
	defer bc.mu.Unlock()

	for _, ds := range bc.sessions {
		ds.cancel()
		ds.mu.Lock()
		if ds.conn != nil {
			_ = ds.conn.Close()
			ds.conn = nil
		}
		ds.mu.Unlock()
	}
	bc.sessions = make(map[string]*DeviceSession)
	bc.deviceSessions = make(map[string]map[string]bool)
}
