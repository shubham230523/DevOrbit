package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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
	SessionID  string
	DeviceID   string
	E2ESession *security.E2ESession
	ExpiresAt  time.Time
	cancel     context.CancelFunc
	mu         sync.Mutex
	conn       *websocket.Conn
}

type BrokerClient struct {
	mu             sync.RWMutex
	BrokerURL      string
	MachineID      string
	SessionID      string // Legacy/single session convenience
	HostToken      string
	Server         *httptransport.Server
	E2ESession     *security.E2ESession
	sessions       map[string]*DeviceSession          // Key: SessionID -> DeviceSession
	deviceSessions map[string]map[string]bool         // Key: DeviceID -> set of SessionIDs
	stopChan       chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
}

func NewBrokerClient(brokerURL, machineID, sessionID, hostToken string, server *httptransport.Server) *BrokerClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &BrokerClient{
		BrokerURL:      brokerURL,
		MachineID:      machineID,
		SessionID:      sessionID,
		HostToken:      hostToken,
		Server:         server,
		sessions:       make(map[string]*DeviceSession),
		deviceSessions: make(map[string]map[string]bool),
		stopChan:       make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
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
		SessionID:  sessionID,
		DeviceID:   deviceID,
		E2ESession: e2eSess,
		ExpiresAt:  expiry,
		cancel:     sessCancel,
	}

	bc.sessions[sessionID] = ds
	if _, ok := bc.deviceSessions[deviceID]; !ok {
		bc.deviceSessions[deviceID] = make(map[string]bool)
	}
	bc.deviceSessions[deviceID][sessionID] = true
	bc.SessionID = sessionID // Update primary session pointer
	bc.E2ESession = e2eSess
	bc.mu.Unlock()

	// Launch dedicated, concurrent session loop for this device session
	go bc.runSessionLoop(sessCtx, ds)

	log.Printf("BrokerClient: Authenticated independent session provisioned for session=%s device=%s (total active: %d)",
		sessionID, deviceID, bc.ActiveSessionCount())
	return nil
}

func (bc *BrokerClient) RevokeSession(sessionID string) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	ds, ok := bc.sessions[sessionID]
	if !ok {
		return
	}

	ds.cancel()
	ds.mu.Lock()
	if ds.conn != nil {
		_ = ds.conn.Close()
		ds.conn = nil
	}
	ds.mu.Unlock()

	delete(bc.sessions, sessionID)
	if m, ok := bc.deviceSessions[ds.DeviceID]; ok {
		delete(m, sessionID)
		if len(m) == 0 {
			delete(bc.deviceSessions, ds.DeviceID)
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
	log.Printf("BrokerClient: Session %s revoked and disconnected (remaining active: %d)", sessionID, len(bc.sessions))
}

func (bc *BrokerClient) RevokeDevice(deviceID string) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	sessionIDs, ok := bc.deviceSessions[deviceID]
	if !ok {
		return
	}

	for sID := range sessionIDs {
		if ds, exists := bc.sessions[sID]; exists {
			ds.cancel()
			ds.mu.Lock()
			if ds.conn != nil {
				_ = ds.conn.Close()
				ds.conn = nil
			}
			ds.mu.Unlock()
			delete(bc.sessions, sID)
		}
	}
	delete(bc.deviceSessions, deviceID)

	if _, exists := bc.sessions[bc.SessionID]; !exists {
		bc.SessionID = ""
		bc.E2ESession = nil
		for remaining, remDS := range bc.sessions {
			bc.SessionID = remaining
			bc.E2ESession = remDS.E2ESession
			break
		}
	}
	log.Printf("BrokerClient: Device %s and all its sessions revoked (remaining active: %d)", deviceID, len(bc.sessions))
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

					if appReq.Method == "" || appReq.Path == "" {
						errResp := ApplicationResponseEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       ds.SessionID,
							RequestID:       appReq.RequestID,
							StatusCode:      http.StatusBadRequest,
							Body:            `{"error":"invalid request envelope: method and path are required"}`,
							SeqNum:          f.SeqNum,
							Timestamp:       time.Now().UnixNano(),
						}
						respBytes, _ := json.Marshal(errResp)
						encryptedRespPayload, _ := ds.E2ESession.Encrypt(security.DirHostToClient, respBytes, f.SeqNum, time.Now().UnixNano())
						ds.mu.Lock()
						if ds.conn != nil {
							_ = ds.conn.WriteJSON(FrameEnvelope{
								ProtocolVersion: "1.2",
								SessionID:       ds.SessionID,
								Type:            "data",
								EncryptedData:   encryptedRespPayload,
								SeqNum:          f.SeqNum,
								Timestamp:       time.Now().UnixNano(),
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

					for k, v := range appReq.Headers {
						req.Header.Set(k, v)
					}

					rec := NewResponseWriterBuffer()
					bc.Server.Mux().ServeHTTP(rec, req)

					appResp := ApplicationResponseEnvelope{
						ProtocolVersion: "1.2",
						SessionID:       ds.SessionID,
						RequestID:       appReq.RequestID,
						StatusCode:      rec.Code,
						Body:            rec.Body.String(),
						SeqNum:          f.SeqNum,
						Timestamp:       time.Now().UnixNano(),
					}

					respBytes, err := json.Marshal(appResp)
					if err != nil {
						return
					}

					encryptedRespPayload, err := ds.E2ESession.Encrypt(security.DirHostToClient, respBytes, f.SeqNum, time.Now().UnixNano())
					if err == nil {
						ds.mu.Lock()
						if ds.conn != nil {
							_ = ds.conn.WriteJSON(FrameEnvelope{
								ProtocolVersion: "1.2",
								SessionID:       ds.SessionID,
								Type:            "data",
								EncryptedData:   encryptedRespPayload,
								SeqNum:          f.SeqNum,
								Timestamp:       time.Now().UnixNano(),
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
	// Active sessions manage their own connection loops via ProvisionSession
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
