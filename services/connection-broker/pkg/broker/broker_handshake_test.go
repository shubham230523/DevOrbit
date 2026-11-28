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

func setupTestBrokerServer(t *testing.T) (*Broker, *httptest.Server, string) {
	b := NewBroker()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)
	server := httptest.NewServer(mux)
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	return b, server, wsURL
}

func TestBroker_ClientConnectsBeforeHost_ReceivesPendingThenReady(t *testing.T) {
	b, server, wsURL := setupTestBrokerServer(t)
	defer server.Close()

	machineID := "mach-pend-01"
	deviceID := "dev-pend-01"
	sessionID := "sess-pend-01"
	hostToken := "host-token-pend-32b"
	clientToken := "client-token-pend-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	// 1. Client connects first
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, clientHeader)
	if err != nil {
		t.Fatalf("Client failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Client should immediately receive session_pending frame
	var pendingFrame FrameEnvelope
	if err := clientConn.ReadJSON(&pendingFrame); err != nil {
		t.Fatalf("Failed to read pending frame: %v", err)
	}
	if pendingFrame.Type != "session_pending" || pendingFrame.Status != "waiting_for_host" {
		t.Fatalf("Expected session_pending frame, got: %+v", pendingFrame)
	}

	// 2. Host connects now
	hostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, err := websocket.DefaultDialer.Dial(hostURL, hostHeader)
	if err != nil {
		t.Fatalf("Host failed to connect: %v", err)
	}
	defer hostConn.Close()

	// Client should now receive session_ready frame
	var readyFrame FrameEnvelope
	if err := clientConn.ReadJSON(&readyFrame); err != nil {
		t.Fatalf("Failed to read ready frame: %v", err)
	}
	if readyFrame.Type != "session_ready" || readyFrame.Status != "ready" {
		t.Fatalf("Expected session_ready frame, got: %+v", readyFrame)
	}
}

func TestBroker_HostConnectsBeforeClient_ClientGetsReady(t *testing.T) {
	b, server, wsURL := setupTestBrokerServer(t)
	defer server.Close()

	machineID := "mach-ready-01"
	deviceID := "dev-ready-01"
	sessionID := "sess-ready-01"
	hostToken := "host-token-ready-32b"
	clientToken := "client-token-ready-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	// 1. Host connects first
	hostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, err := websocket.DefaultDialer.Dial(hostURL, hostHeader)
	if err != nil {
		t.Fatalf("Host failed to connect: %v", err)
	}
	defer hostConn.Close()

	// 2. Client connects
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, clientHeader)
	if err != nil {
		t.Fatalf("Client failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Client should immediately receive session_ready frame
	var readyFrame FrameEnvelope
	if err := clientConn.ReadJSON(&readyFrame); err != nil {
		t.Fatalf("Failed to read ready frame: %v", err)
	}
	if readyFrame.Type != "session_ready" || readyFrame.Status != "ready" {
		t.Fatalf("Expected session_ready frame, got: %+v", readyFrame)
	}
}

func TestBroker_DifferentSessions_NoReadiness(t *testing.T) {
	b, server, wsURL := setupTestBrokerServer(t)
	defer server.Close()

	machineID := "mach-diff-01"
	deviceID := "dev-diff-01"
	sessionIDHost := "sess-host-diff-01"
	sessionIDClient := "sess-client-diff-01"
	hostToken := "host-token-diff-32b"
	clientToken := "client-token-diff-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionIDClient, clientToken)

	// Host connects to sessionIDHost
	hostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionIDHost)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, err := websocket.DefaultDialer.Dial(hostURL, hostHeader)
	if err != nil {
		t.Fatalf("Host failed to connect: %v", err)
	}
	defer hostConn.Close()

	// Client connects to sessionIDClient
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionIDClient)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, clientHeader)
	if err != nil {
		t.Fatalf("Client failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Client receives session_pending, NOT session_ready
	var frame FrameEnvelope
	if err := clientConn.ReadJSON(&frame); err != nil {
		t.Fatalf("Failed to read frame: %v", err)
	}
	if frame.Type != "session_pending" {
		t.Fatalf("Expected session_pending for mismatched sessions, got: %s", frame.Type)
	}

	// Wait briefly to confirm no session_ready arrives
	clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var nextFrame FrameEnvelope
	err = clientConn.ReadJSON(&nextFrame)
	if err == nil {
		t.Fatalf("Unexpected frame received: %+v", nextFrame)
	}
}

func TestBroker_HostDisconnectAfterReadiness_ClientNotified(t *testing.T) {
	b, server, wsURL := setupTestBrokerServer(t)
	defer server.Close()

	machineID := "mach-hdisc-01"
	deviceID := "dev-hdisc-01"
	sessionID := "sess-hdisc-01"
	hostToken := "host-token-hdisc-32b"
	clientToken := "client-token-hdisc-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	// Host connects
	hostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, err := websocket.DefaultDialer.Dial(hostURL, hostHeader)
	if err != nil {
		t.Fatalf("Host failed to connect: %v", err)
	}

	// Client connects
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, clientHeader)
	if err != nil {
		t.Fatalf("Client failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Read session_ready
	var ready FrameEnvelope
	if err := clientConn.ReadJSON(&ready); err != nil || ready.Type != "session_ready" {
		t.Fatalf("Expected session_ready, got: %+v, err: %v", ready, err)
	}

	// Now Host drops connection
	_ = hostConn.Close()

	// Client must receive host_disconnected frame
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	var discFrame FrameEnvelope
	if err := clientConn.ReadJSON(&discFrame); err != nil {
		t.Fatalf("Expected host_disconnected frame, got err: %v", err)
	}
	if discFrame.Type != "host_disconnected" || discFrame.Status != "waiting_for_host" {
		t.Fatalf("Expected host_disconnected frame, got: %+v", discFrame)
	}
}

func TestBroker_RevocationDuringSession_ClientRevoked(t *testing.T) {
	b, server, wsURL := setupTestBrokerServer(t)
	defer server.Close()

	machineID := "mach-revact-01"
	deviceID := "dev-revact-01"
	sessionID := "sess-revact-01"
	hostToken := "host-token-revact-32b"
	clientToken := "client-token-revact-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	// Connect host & client
	hostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, _ := websocket.DefaultDialer.Dial(hostURL, hostHeader)
	defer hostConn.Close()

	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, _ := websocket.DefaultDialer.Dial(clientURL, clientHeader)
	defer clientConn.Close()

	var ready FrameEnvelope
	_ = clientConn.ReadJSON(&ready)

	// Revoke device while active
	b.RevokeDevice(machineID, deviceID)

	// Client should receive revoked frame
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	var revFrame FrameEnvelope
	if err := clientConn.ReadJSON(&revFrame); err != nil {
		t.Fatalf("Expected revoked frame, got err: %v", err)
	}
	if revFrame.Type != "revoked" || revFrame.Status != "revoked" {
		t.Fatalf("Expected revoked frame, got: %+v", revFrame)
	}
}
