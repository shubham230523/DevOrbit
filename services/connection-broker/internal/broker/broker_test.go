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

func TestBroker_HostAndClientAuthenticationEnforcement(t *testing.T) {
	b := NewBroker()
	b.AuthorizeHost("mach-auth-01", "valid-host-token-secret")

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// 1. Attempt connection for COMPLETELY UNREGISTERED host -> Should receive 401
	unregisteredHostURL := fmt.Sprintf("%s/ws/host?machine_id=mach-unregistered&session_id=sess-1&token=some-token", wsURL)
	_, res, err := websocket.DefaultDialer.Dial(unregisteredHostURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected HTTP 401 Unauthorized for unregistered host registration attempt")
	}

	// 2. Attempt registered host with WRONG token -> Should receive 401
	badHostURL := fmt.Sprintf("%s/ws/host?machine_id=mach-auth-01&session_id=sess-1&token=wrong-token", wsURL)
	_, res, err = websocket.DefaultDialer.Dial(badHostURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected HTTP 401 Unauthorized for invalid host token attempt")
	}

	// 3. Attempt unauthorized client device connection -> Should receive 403
	badClientURL := fmt.Sprintf("%s/ws/client?machine_id=mach-auth-01&device_id=unauthorized-phone&session_id=sess-1&token=unauthorized-token", wsURL)
	_, res, err = websocket.DefaultDialer.Dial(badClientURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for unauthorized client device connection attempt")
	}

	// 4. Authorize device & host connection
	b.AuthorizeDevice("mach-auth-01", "dev-phone-01", "sess-1", "valid-client-token")
	goodHostURL := fmt.Sprintf("%s/ws/host?machine_id=mach-auth-01&session_id=sess-1&token=valid-host-token-secret", wsURL)
	hostConn, res, err := websocket.DefaultDialer.Dial(goodHostURL, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected successful host WSS upgrade, got: %v", err)
	}
	defer hostConn.Close()

	goodClientURL := fmt.Sprintf("%s/ws/client?machine_id=mach-auth-01&device_id=dev-phone-01&session_id=sess-1&token=valid-client-token", wsURL)
	clientConn, res, err := websocket.DefaultDialer.Dial(goodClientURL, nil)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected successful client WSS upgrade, got: %v", err)
	}
	defer clientConn.Close()

	time.Sleep(50 * time.Millisecond)

	// 5. Test Bidirectional Opaque Payload Frame Relaying
	opaquePayload := "a1b2c3d4_opaque_e2e_ciphertext_payload"
	clientFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       "sess-1",
		Type:            "data",
		EncryptedData:   opaquePayload,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := clientConn.WriteJSON(&clientFrame); err != nil {
		t.Fatalf("Failed to write frame from client: %v", err)
	}

	var hostFrame FrameEnvelope
	if err := hostConn.ReadJSON(&hostFrame); err != nil {
		t.Fatalf("Failed to read frame on host: %v", err)
	}

	if hostFrame.EncryptedData != opaquePayload {
		t.Errorf("Payload mismatch! Expected opaque frame '%s', got '%s'", opaquePayload, hostFrame.EncryptedData)
	}

	// 6. Test Device Revocation Teardown
	b.RevokeDevice("mach-auth-01", "dev-phone-01")
	time.Sleep(50 * time.Millisecond)

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected 0 active sessions after device revocation")
	}
}
