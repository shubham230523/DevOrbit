package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type brokerTestSecretStore struct {
	available bool
	secrets   map[string]string
}

func (b *brokerTestSecretStore) Name() string    { return "broker-test-store" }
func (b *brokerTestSecretStore) Available() bool { return b.available }
func (b *brokerTestSecretStore) Store(scope, id string, secret []byte) (string, error) {
	if !b.available {
		return "", security.ErrSecureStoreUnavailable
	}
	b.secrets[scope+":"+id] = string(secret)
	return "test-ref:" + scope + ":" + id, nil
}
func (b *brokerTestSecretStore) Load(scope, id, ref string) ([]byte, error) {
	if !b.available {
		return nil, security.ErrSecureStoreUnavailable
	}
	v, ok := b.secrets[scope+":"+id]
	if !ok {
		return nil, security.ErrSecretNotFound
	}
	return []byte(v), nil
}
func (b *brokerTestSecretStore) Delete(scope, id, ref string) error {
	delete(b.secrets, scope+":"+id)
	return nil
}

func TestBrokerClient_PersistenceNoPlaintextLegacyRejectAndRevocationRestart(t *testing.T) {
	persistPath := filepath.Join(t.TempDir(), "host_sessions.json")
	ss := &brokerTestSecretStore{available: true, secrets: make(map[string]string)}

	bc1 := NewBrokerClient("ws://127.0.0.1:18081", "mach-p1", "", "host-token-32b", nil)
	bc1.SetSecretStore(ss)
	if err := bc1.EnableSessionPersistence(persistPath); err != nil {
		t.Fatalf("EnableSessionPersistence failed: %v", err)
	}

	secret1 := "broker-session-secret-01-32-bytes!"
	secret2 := "broker-session-secret-02-32-bytes!"
	if err := bc1.ProvisionSession("sess-b1", "dev-b1", secret1, time.Hour); err != nil {
		t.Fatalf("ProvisionSession b1 failed: %v", err)
	}
	if err := bc1.ProvisionSession("sess-b2", "dev-b2", secret2, time.Hour); err != nil {
		t.Fatalf("ProvisionSession b2 failed: %v", err)
	}

	diskBytes, err := os.ReadFile(persistPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(diskBytes), secret1) || strings.Contains(string(diskBytes), secret2) || strings.Contains(string(diskBytes), `"session_secret"`) {
		t.Fatalf("SECURITY FAILURE: BrokerClient persistence wrote plaintext session_secret to disk")
	}

	bc1.RevokeSession("sess-b1")
	bc1.Stop()

	// Restart into bc2: only sess-b2 is restored; sess-b1 stays revoked
	bc2 := NewBrokerClient("ws://127.0.0.1:18081", "mach-p1", "", "host-token-32b", nil)
	bc2.SetSecretStore(ss)
	if err := bc2.EnableSessionPersistence(persistPath); err != nil {
		t.Fatalf("EnableSessionPersistence bc2 failed: %v", err)
	}
	if err := bc2.LoadPersistedSessions(); err != nil {
		t.Fatalf("LoadPersistedSessions failed: %v", err)
	}
	defer bc2.Stop()

	if bc2.ActiveSessionCount() != 1 {
		t.Fatalf("expected exactly 1 active session after restart with revocation, got %d", bc2.ActiveSessionCount())
	}
	if err := bc2.ProvisionSession("sess-b1", "dev-b1", secret1, time.Hour); !errors.Is(err, security.ErrSessionRevoked) {
		t.Fatalf("expected ErrSessionRevoked when attempting to reuse revoked session ID after restart, got: %v", err)
	}

	// Legacy plaintext broker file must be rejected fail-closed
	legacyPath := filepath.Join(t.TempDir(), "legacy_host_sessions.json")
	_ = os.WriteFile(legacyPath, []byte(`[{"session_id":"sess-old","device_id":"dev-old","session_secret":"plain-secret","expires_at":"2099-01-01T00:00:00Z"}]`), 0600)
	bcLegacy := NewBrokerClient("ws://127.0.0.1:18081", "mach-p1", "", "host-token-32b", nil)
	bcLegacy.SetSecretStore(ss)
	_ = bcLegacy.EnableSessionPersistence(legacyPath)
	if err := bcLegacy.LoadPersistedSessions(); !errors.Is(err, security.ErrCorruptPersistedState) {
		t.Fatalf("expected ErrCorruptPersistedState for legacy plaintext broker file, got: %v", err)
	}
	if bcLegacy.ActiveSessionCount() != 0 {
		t.Fatalf("legacy plaintext broker sessions must not be restored")
	}
}
