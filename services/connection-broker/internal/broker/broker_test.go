package broker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBroker_RealWebSocketServerRelayAndOpaquePayload(t *testing.T) {
	b := NewBroker()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	machineID := "mach-real-01"
	deviceID := "dev-phone-01"
	sessionID := "sess-real-100"

	b.AuthorizeDevice(machineID, deviceID)

	// 1. Host establishes WebSocket Connection
	hostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostConn, _, err := websocket.DefaultDialer.Dial(hostURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial host WSS: %v", err)
	}
	defer hostConn.Close()

	// 2. Client establishes WebSocket Connection
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, nil)
	if err != nil {
		t.Fatalf("Failed to dial client WSS: %v", err)
	}
	defer clientConn.Close()

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	if b.ActiveSessionCount() != 1 {
		t.Errorf("Expected 1 active session on broker, got %d", b.ActiveSessionCount())
	}

	// 3. Client sends E2E Encrypted Payload Frame to Broker -> Broker Relays to Host
	opaqueCipherPayload := "a1b2c3d4_opaque_encrypted_aes_gcm_bytes_here"
	clientFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   opaqueCipherPayload,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := clientConn.WriteJSON(&clientFrame); err != nil {
		t.Fatalf("Failed to write frame from client: %v", err)
	}

	// Read on Host WSS
	var hostReceivedFrame FrameEnvelope
	if err := hostConn.ReadJSON(&hostReceivedFrame); err != nil {
		t.Fatalf("Failed to read frame on host: %v", err)
	}

	if hostReceivedFrame.EncryptedData != opaqueCipherPayload {
		t.Errorf("Opaque relay failed! Host received '%s', expected '%s'", hostReceivedFrame.EncryptedData, opaqueCipherPayload)
	}

	// 4. Test Device Revocation Teardown over real WebSocket
	b.RevokeDevice(machineID, deviceID)
	time.Sleep(50 * time.Millisecond)

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected active sessions to be 0 after device revocation")
	}
}
