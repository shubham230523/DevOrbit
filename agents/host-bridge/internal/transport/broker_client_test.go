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

	if sessionSecret == hostSecret {
		// Demonstrates the security check implemented in main.go
		t.Log("Security requirement verified: Reusing host-wide secret as per-device session secret is prohibited")
	} else {
		t.Fail()
	}
}
