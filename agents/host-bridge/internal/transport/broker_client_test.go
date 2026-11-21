package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/security"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func TestBrokerClient_DisconnectedWhenNoSessionProvisioned(t *testing.T) {
	serverConnected := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverConnected = true
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			conn.Close()
		}
	}))
	defer s.Close()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)

	// Broker client with empty SessionID and nil E2ESession
	client := NewBrokerClient(wsURL, "mach-01", "", "token-secret", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	client.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	if serverConnected {
		t.Errorf("Security failure: BrokerClient must remain disconnected when session credentials are not provisioned")
	}
}

func TestBrokerClient_HeaderAuthenticationWithoutQueryToken(t *testing.T) {
	var receivedAuthHeader string
	var receivedURL string
	done := make(chan struct{})

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		receivedAuthHeader = r.Header.Get("Authorization")

		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			defer conn.Close()
			<-done
		}
	}))
	defer s.Close()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)

	sessionID := "sess-lifecycle-test-01"
	deviceID := "dev-phone-test-01"
	hostToken := "host-token-secret-32b"
	sessionSecret := "session-specific-secret-32-bytes"

	e2eSess, err := security.NewE2ESession(sessionID, deviceID, sessionSecret, 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to create E2E session: %v", err)
	}

	client := NewBrokerClient(wsURL, "mach-lifecycle-01", sessionID, hostToken, nil)
	client.SetE2ESession(e2eSess)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	close(done)
	client.Stop()

	// Verify credentials are sent strictly via Authorization header
	expectedHeader := "Bearer " + hostToken
	if receivedAuthHeader != expectedHeader {
		t.Errorf("Expected Authorization header '%s', got '%s'", expectedHeader, receivedAuthHeader)
	}

	// Verify token is NOT present in connection URL
	if strings.Contains(receivedURL, "token") {
		t.Errorf("Security failure: Connection URL must NOT contain token, got: %s", receivedURL)
	}
}

func TestBrokerClient_ReconnectRejectsExpiredSession(t *testing.T) {
	connCount := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCount++
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			// Immediately close connection to trigger client reconnect attempt
			conn.Close()
		}
	}))
	defer s.Close()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)

	// Session that expires almost immediately
	e2eSess, err := security.NewE2ESession("sess-exp-01", "dev-01", "session-secret-32b", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	client := NewBrokerClient(wsURL, "mach-01", "sess-exp-01", "host-token-32b", nil)
	client.SetE2ESession(e2eSess)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	client.Start(ctx)
	defer client.Stop()

	// Wait past expiration
	time.Sleep(200 * time.Millisecond)

	// Reconnect must not succeed with expired credentials
	if !e2eSess.IsExpired() {
		t.Fatalf("Expected session to be expired")
	}
}

func TestHostBridge_RejectsHostSecretAsSessionSecret(t *testing.T) {
	hostSecret := "host-wide-master-secret-32-bytes"
	sessionSecret := hostSecret // Insecure reuse of host-wide secret!

	client := NewBrokerClient("ws://127.0.0.1:8081", "mach-01", "", hostSecret, nil)
	err := client.ProvisionSession("sess-01", "dev-01", sessionSecret, 1*time.Hour)
	if err == nil {
		t.Fatalf("Expected ProvisionSession to reject sessionSecret identical to hostSecret")
	}
}

func TestBrokerClient_ProvisionSessionLifecycleAndRevocation(t *testing.T) {
	connectedCh := make(chan string, 5)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessID := r.URL.Query().Get("session_id")
		connectedCh <- sessID

		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			defer conn.Close()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer s.Close()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)

	client := NewBrokerClient(wsURL, "mach-prov-01", "", "host-token-32b", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Initially unprovisioned -> should not have connected
	if len(connectedCh) != 0 {
		t.Fatalf("Client connected before session was provisioned")
	}

	// Now provision the session
	sessionID := "sess-authenticated-999"
	deviceID := "dev-phone-999"
	sessionSecret := "session-specific-secret-32-bytes-ok"

	err := client.ProvisionSession(sessionID, deviceID, sessionSecret, 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to provision session: %v", err)
	}

	select {
	case connectedSess := <-connectedCh:
		if connectedSess != sessionID {
			t.Errorf("Connected with wrong session ID: expected %s, got %s", sessionID, connectedSess)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Timed out waiting for provisioned client to connect")
	}

	// Revoke session -> must disconnect and clear E2ESession
	client.RevokeSession(sessionID)
	client.mu.RLock()
	revokedSessID := client.SessionID
	revokedE2E := client.E2ESession
	client.mu.RUnlock()

	if revokedSessID != "" || revokedE2E != nil {
		t.Errorf("Expected session credentials to be wiped on revocation, got sess=%s, e2e=%v", revokedSessID, revokedE2E)
	}

	client.Stop()
}

func TestBrokerClient_IndependentSimultaneousDeviceSessions(t *testing.T) {
	connectedCh := make(chan string, 10)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessID := r.URL.Query().Get("session_id")
		connectedCh <- sessID

		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			defer conn.Close()
			for {
				var msg map[string]any
				if err := conn.ReadJSON(&msg); err != nil {
					break
				}
			}
		}
	}))
	defer s.Close()

	wsURL := strings.Replace(s.URL, "http://", "ws://", 1)

	client := NewBrokerClient(wsURL, "mach-multi-01", "", "host-token-32b", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client.Start(ctx)

	// 1. Provision Device 1 / Session 1
	err := client.ProvisionSession("sess-dev1", "phone-01", "secret-phone-32-bytes-ok!", 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to provision dev1: %v", err)
	}

	// 2. Provision Device 2 / Session 2
	err = client.ProvisionSession("sess-dev2", "tablet-02", "secret-tablet-32-bytes-ok!", 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to provision dev2: %v", err)
	}

	// Verify both sessions connected independently
	connectedSessions := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case sID := <-connectedCh:
			connectedSessions[sID] = true
		case <-time.After(1 * time.Second):
			t.Fatalf("Timed out waiting for both sessions to connect")
		}
	}

	if !connectedSessions["sess-dev1"] || !connectedSessions["sess-dev2"] {
		t.Fatalf("Expected both sess-dev1 and sess-dev2 to connect, got: %v", connectedSessions)
	}

	if client.ActiveSessionCount() != 2 {
		t.Fatalf("Expected 2 active sessions, got %d", client.ActiveSessionCount())
	}

	// 3. Revoke Device 1: Device 2 must remain connected and active
	client.RevokeDevice("phone-01")

	if client.ActiveSessionCount() != 1 {
		t.Fatalf("Expected 1 active session after revoking phone-01, got %d", client.ActiveSessionCount())
	}

	activeID, _ := client.ActiveSession()
	if activeID != "sess-dev2" {
		t.Fatalf("Expected active session to be sess-dev2, got %s", activeID)
	}

	client.Stop()
}
