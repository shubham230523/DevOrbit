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

func TestBroker_AuthenticatedEndToEndWebSocketRelay(t *testing.T) {
	b := NewBroker()
	machineID := "mach-e2e-01"
	deviceID := "dev-phone-01"
	sessionID := "sess-e2e-100"
	hostToken := "host-auth-token-secret-32b"
	clientToken := "client-auth-token-secret-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// 1. Host Bridge dials /ws/host with valid token
	hostDialURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s&token=%s", wsURL, machineID, sessionID, hostToken)
	hostConn, _, err := websocket.DefaultDialer.Dial(hostDialURL, nil)
	if err != nil {
		t.Fatalf("Failed host WSS connection: %v", err)
	}
	defer hostConn.Close()

	// 2. Client dials /ws/client
	clientDialURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s&token=%s", wsURL, machineID, deviceID, sessionID, clientToken)
	clientConn, _, err := websocket.DefaultDialer.Dial(clientDialURL, nil)
	if err != nil {
		t.Fatalf("Failed client WSS connection: %v", err)
	}
	defer clientConn.Close()

	time.Sleep(50 * time.Millisecond)

	// 3. Client sends E2E Encrypted Payload Frame to Broker -> Broker Relays to Host
	opaqueCiphertext := "a1b2c3d4_opaque_e2e_encrypted_bytes_here"
	clientReqFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   opaqueCiphertext,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := clientConn.WriteJSON(&clientReqFrame); err != nil {
		t.Fatalf("Failed to send frame from client: %v", err)
	}

	// Host reads relayed frame from broker
	var hostFrame FrameEnvelope
	if err := hostConn.ReadJSON(&hostFrame); err != nil {
		t.Fatalf("Host failed to read relayed frame: %v", err)
	}

	if hostFrame.EncryptedData != opaqueCiphertext {
		t.Errorf("Opaque relay failed! Host received '%s', expected '%s'", hostFrame.EncryptedData, opaqueCiphertext)
	}

	// 4. Host sends encrypted response back to client via broker
	opaqueRespCiphertext := "e5f6g7h8_opaque_e2e_encrypted_response_bytes"
	hostRespFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   opaqueRespCiphertext,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := hostConn.WriteJSON(&hostRespFrame); err != nil {
		t.Fatalf("Host failed to send response frame: %v", err)
	}

	var clientFrame FrameEnvelope
	if err := clientConn.ReadJSON(&clientFrame); err != nil {
		t.Fatalf("Client failed to read response frame: %v", err)
	}

	if clientFrame.EncryptedData != opaqueRespCiphertext {
		t.Errorf("Client response mismatch! Expected '%s', got '%s'", opaqueRespCiphertext, clientFrame.EncryptedData)
	}

	// 5. Test Revocation Teardown
	b.RevokeDevice(machineID, deviceID)
	time.Sleep(50 * time.Millisecond)

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected active sessions to be 0 after revocation")
	}
}
