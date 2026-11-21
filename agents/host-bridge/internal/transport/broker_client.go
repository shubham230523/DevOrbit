package transport

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type BrokerClient struct {
	BrokerURL string
	MachineID string
	SessionID string
	conn      *websocket.Conn
	stopChan  chan struct{}
}

func NewBrokerClient(brokerURL, machineID, sessionID string) *BrokerClient {
	return &BrokerClient{
		BrokerURL: brokerURL,
		MachineID: machineID,
		SessionID: sessionID,
		stopChan:  make(chan struct{}),
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

			wsURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", bc.BrokerURL, bc.MachineID, bc.SessionID)
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
				_, _, err := conn.ReadMessage()
				if err != nil {
					log.Printf("HostBridge BrokerClient: Disconnected from broker WSS (%v)", err)
					conn.Close()
					break
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
