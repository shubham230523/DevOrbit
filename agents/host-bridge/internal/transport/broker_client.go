package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http/httptest"
	"strings"
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
	BrokerURL  string
	MachineID  string
	SessionID  string
	HostToken  string
	Server     *httptransport.Server
	E2ESession *security.E2ESession
	conn       *websocket.Conn
	stopChan   chan struct{}
}

func NewBrokerClient(brokerURL, machineID, sessionID, hostToken string, server *httptransport.Server) *BrokerClient {
	return &BrokerClient{
		BrokerURL: brokerURL,
		MachineID: machineID,
		SessionID: sessionID,
		HostToken: hostToken,
		Server:    server,
		stopChan:  make(chan struct{}),
	}
}

func (bc *BrokerClient) SetE2ESession(sess *security.E2ESession) {
	bc.E2ESession = sess
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

			wsURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s&token=%s", bc.BrokerURL, bc.MachineID, bc.SessionID, bc.HostToken)
			conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
			if err != nil {
				log.Printf("HostBridge BrokerClient: Connection failed (%v), retrying in %v...", err, backoff)
				time.Sleep(backoff)
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			bc.conn = conn
			backoff = 1 * time.Second
			log.Printf("HostBridge BrokerClient: Successfully connected to broker WSS at %s", bc.BrokerURL)

			// Read loop
			for {
				var frame FrameEnvelope
				if err := conn.ReadJSON(&frame); err != nil {
					log.Printf("HostBridge BrokerClient: Disconnected from broker WSS (%v)", err)
					conn.Close()
					break
				}

				if frame.Type == "ping" {
					_ = conn.WriteJSON(FrameEnvelope{
						ProtocolVersion: "1.2",
						SessionID:       bc.SessionID,
						Type:            "pong",
						Timestamp:       time.Now().UnixNano(),
					})
					continue
				}

				// Process incoming E2E encrypted application request envelope
				if frame.Type == "data" && bc.E2ESession != nil && bc.Server != nil {
					plainReqBytes, err := bc.E2ESession.Decrypt(security.DirClientToHost, frame.EncryptedData, frame.SeqNum, frame.Timestamp)
					if err != nil {
						log.Printf("HostBridge BrokerClient: Decryption failed for frame seq=%d: %v", frame.SeqNum, err)
						continue
					}

					var appReq ApplicationRequestEnvelope
					if err := json.Unmarshal(plainReqBytes, &appReq); err != nil {
						log.Printf("HostBridge BrokerClient: Failed to unmarshal ApplicationRequestEnvelope: %v", err)
						continue
					}

					// Route request directly using Server's thread-safe Mux
					method := appReq.Method
					if method == "" {
						method = "GET"
					}
					path := appReq.Path
					if path == "" {
						path = "/api/v1/health"
					}

					req := httptest.NewRequest(method, path, strings.NewReader(appReq.Body))
					for k, v := range appReq.Headers {
						req.Header.Set(k, v)
					}

					rec := httptest.NewRecorder()
					bc.Server.Mux().ServeHTTP(rec, req)

					appResp := ApplicationResponseEnvelope{
						ProtocolVersion: "1.2",
						SessionID:       bc.SessionID,
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

					encryptedRespPayload, err := bc.E2ESession.Encrypt(security.DirHostToClient, respBytes, frame.SeqNum, time.Now().UnixNano())
					if err == nil {
						_ = conn.WriteJSON(FrameEnvelope{
							ProtocolVersion: "1.2",
							SessionID:       bc.SessionID,
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
	if bc.conn != nil {
		_ = bc.conn.Close()
	}
}
