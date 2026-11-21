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

type BrokerClient struct {
	mu                 sync.RWMutex
	BrokerURL          string
	MachineID          string
	SessionID          string
	HostToken          string
	Server             *httptransport.Server
	E2ESession         *security.E2ESession
	conn               *websocket.Conn
	stopChan           chan struct{}
	sessionProvisioned chan struct{}
}

func NewBrokerClient(brokerURL, machineID, sessionID, hostToken string, server *httptransport.Server) *BrokerClient {
	return &BrokerClient{
		BrokerURL:          brokerURL,
		MachineID:          machineID,
		SessionID:          sessionID,
		HostToken:          hostToken,
		Server:             server,
		stopChan:           make(chan struct{}),
		sessionProvisioned: make(chan struct{}, 1),
	}
}

func (bc *BrokerClient) SetE2ESession(sess *security.E2ESession) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.E2ESession = sess
}

func (bc *BrokerClient) ActiveSession() (string, *security.E2ESession) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.SessionID, bc.E2ESession
}

func (bc *BrokerClient) ProvisionSession(sessionID, deviceID, sessionSecret string, ttl time.Duration) error {
	if sessionID == "" || deviceID == "" || sessionSecret == "" {
		return errors.New("provisioning failed: sessionID, deviceID, and sessionSecret are required")
	}
	if sessionSecret == bc.HostToken {
		return errors.New("security error: per-device session secret cannot be identical to host-wide secret")
	}

	e2eSess, err := security.NewE2ESession(sessionID, deviceID, sessionSecret, ttl)
	if err != nil {
		return fmt.Errorf("failed to create provisioned E2E session: %w", err)
	}

	bc.mu.Lock()
	bc.SessionID = sessionID
	bc.E2ESession = e2eSess
	if bc.conn != nil {
		_ = bc.conn.Close()
		bc.conn = nil
	}
	bc.mu.Unlock()

	// Notify connection loop that session has been provisioned
	select {
	case bc.sessionProvisioned <- struct{}{}:
	default:
	}

	log.Printf("BrokerClient: Authenticated session provisioned successfully for session=%s device=%s", sessionID, deviceID)
	return nil
}

func (bc *BrokerClient) RevokeSession(sessionID string) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.SessionID == sessionID || sessionID == "" {
		bc.E2ESession = nil
		bc.SessionID = ""
		if bc.conn != nil {
			_ = bc.conn.Close()
			bc.conn = nil
		}
		log.Printf("BrokerClient: Session %s revoked and disconnected", sessionID)
	}
}

func (bc *BrokerClient) Start(ctx context.Context) {
	go func() {
		backoff := 1 * time.Second
		maxBackoff := 30 * time.Second

		for {
			select {
			case <-ctx.Done():
				return
			case <-bc.stopChan:
				return
			default:
			}

			bc.mu.RLock()
			machineID := bc.MachineID
			sessionID := bc.SessionID
			hostToken := bc.HostToken
			e2eSess := bc.E2ESession
			bc.mu.RUnlock()

			if machineID == "" || sessionID == "" || hostToken == "" || e2eSess == nil || e2eSess.IsExpired() {
				// Wait until a session is provisioned through authenticated pairing
				select {
				case <-ctx.Done():
					return
				case <-bc.stopChan:
					return
				case <-bc.sessionProvisioned:
					continue
				}
			}

			wsURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", bc.BrokerURL, machineID, sessionID)
			headers := http.Header{
				"Authorization": []string{"Bearer " + hostToken},
			}
			conn, res, err := websocket.DefaultDialer.DialContext(ctx, wsURL, headers)
			if err != nil {
				if res != nil && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) {
					log.Printf("HostBridge BrokerClient: Connection rejected by broker with HTTP %d (revoked or expired). Disconnecting session.", res.StatusCode)
					bc.RevokeSession(sessionID)
					continue
				}
				log.Printf("HostBridge BrokerClient: Connection failed (%v), retrying in %v...", err, backoff)
				time.Sleep(backoff)
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			bc.mu.Lock()
			bc.conn = conn
			bc.mu.Unlock()
			backoff = 1 * time.Second
			log.Printf("HostBridge BrokerClient: Successfully connected to broker WSS at %s for session %s", bc.BrokerURL, sessionID)

			for {
				var frame FrameEnvelope
				if err := conn.ReadJSON(&frame); err != nil {
					log.Printf("HostBridge BrokerClient: Disconnected from broker WSS (%v)", err)
					conn.Close()
					bc.mu.Lock()
					if bc.conn == conn {
						bc.conn = nil
					}
					bc.mu.Unlock()
					break
				}

				if frame.Type == "ping" {
					_ = conn.WriteJSON(FrameEnvelope{
						ProtocolVersion: "1.2",
						SessionID:       sessionID,
						Type:            "pong",
						Timestamp:       time.Now().UnixNano(),
					})
					continue
				}

				bc.mu.RLock()
				activeE2E := bc.E2ESession
				bc.mu.RUnlock()

				if frame.Type == "data" && activeE2E != nil && bc.Server != nil {
					plainReqBytes, err := activeE2E.Decrypt(security.DirClientToHost, frame.EncryptedData, frame.SeqNum, frame.Timestamp)
					if err != nil {
						log.Printf("HostBridge BrokerClient: Decryption failed for frame seq=%d: %v", frame.SeqNum, err)
						continue
					}

					var appReq ApplicationRequestEnvelope
					if err := json.Unmarshal(plainReqBytes, &appReq); err != nil {
						log.Printf("HostBridge BrokerClient: Failed to unmarshal ApplicationRequestEnvelope: %v", err)
						continue
					}

					// Strict Request Validation: reject empty method or path
					if appReq.Method == "" || appReq.Path == "" {
						errResp := ApplicationResponseEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       sessionID,
							RequestID:       appReq.RequestID,
							StatusCode:      http.StatusBadRequest,
							Body:            `{"error":"invalid request envelope: method and path are required"}`,
							SeqNum:          frame.SeqNum,
							Timestamp:       time.Now().UnixNano(),
						}
						respBytes, _ := json.Marshal(errResp)
						encryptedRespPayload, _ := activeE2E.Encrypt(security.DirHostToClient, respBytes, frame.SeqNum, time.Now().UnixNano())
						_ = conn.WriteJSON(FrameEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       sessionID,
							Type:            "data",
							EncryptedData:   encryptedRespPayload,
							SeqNum:          frame.SeqNum,
							Timestamp:       time.Now().UnixNano(),
						})
						continue
					}

					req, err := http.NewRequest(appReq.Method, appReq.Path, strings.NewReader(appReq.Body))
					if err != nil {
						log.Printf("HostBridge BrokerClient: Invalid request (%s %s): %v", appReq.Method, appReq.Path, err)
						continue
					}

					for k, v := range appReq.Headers {
						req.Header.Set(k, v)
					}

					rec := NewResponseWriterBuffer()
					bc.Server.Mux().ServeHTTP(rec, req)

					appResp := ApplicationResponseEnvelope{
						ProtocolVersion: "1.2",
						SessionID:       sessionID,
						RequestID:       appReq.RequestID,
						StatusCode:      rec.Code,
						Body:            rec.Body.String(),
						SeqNum:          frame.SeqNum,
						Timestamp:       time.Now().UnixNano(),
					}

					respBytes, err := json.Marshal(appResp)
					if err != nil {
						continue
					}

					encryptedRespPayload, err := activeE2E.Encrypt(security.DirHostToClient, respBytes, frame.SeqNum, time.Now().UnixNano())
					if err == nil {
						_ = conn.WriteJSON(FrameEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       sessionID,
							Type:            "data",
							EncryptedData:   encryptedRespPayload,
							SeqNum:          frame.SeqNum,
							Timestamp:       time.Now().UnixNano(),
						})
					}
				}
			}
		}
	}()
}

func (bc *BrokerClient) Stop() {
	close(bc.stopChan)
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.conn != nil {
		_ = bc.conn.Close()
		bc.conn = nil
	}
}
