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

func TestBroker_HeaderAuthenticationAndQueryTokenRejection(t *testing.T) {
	b := NewBroker()
	machineID := "mach-auth-01"
	deviceID := "dev-phone-01"
	sessionID := "sess-test-01"
	hostToken := "valid-host-token-secret-32b"
	clientToken := "valid-client-token-secret-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// 1. Missing credentials -> Should receive 401 Unauthorized
	missingCredsURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	_, res, err := websocket.DefaultDialer.Dial(missingCredsURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected HTTP 401 Unauthorized for missing credentials, got: %v", res)
	}

	// 2. Query-string-only credentials -> MUST FAIL with 400 Bad Request
	queryOnlyHostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s&token=%s", wsURL, machineID, sessionID, hostToken)
	_, res, err = websocket.DefaultDialer.Dial(queryOnlyHostURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected HTTP 400 Bad Request for query-string-only credentials on host, got: %v", res)
	}

	queryOnlyClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s&token=%s", wsURL, machineID, deviceID, sessionID, clientToken)
	_, res, err = websocket.DefaultDialer.Dial(queryOnlyClientURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected HTTP 400 Bad Request for query-string-only credentials on client, got: %v", res)
	}

	// 3. Query-string credential present EVEN IF header is provided -> MUST FAIL with 400 Bad Request
	headerHost := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	_, res, err = websocket.DefaultDialer.Dial(queryOnlyHostURL, headerHost)
	if err == nil || res == nil || res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected HTTP 400 Bad Request when credentials present in query string even with header, got: %v", res)
	}

	// 4. Invalid credentials in header -> Should fail (401 for host, 403 for client)
	cleanHostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	badHostHeader := http.Header{"Authorization": []string{"Bearer wrong-host-token"}}
	_, res, err = websocket.DefaultDialer.Dial(cleanHostURL, badHostHeader)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected HTTP 401 Unauthorized for wrong host token in header, got: %v", res)
	}

	cleanClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	badClientHeader := http.Header{"Authorization": []string{"Bearer wrong-client-token"}}
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, badClientHeader)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for wrong client token in header, got: %v", res)
	}

	// 5. Valid Authorization: Bearer <token> Header -> Succeeds (NO token in URL)
	if strings.Contains(cleanHostURL, "token") || strings.Contains(cleanClientURL, "token") {
		t.Fatalf("Security failure: credentials must never be in connection URLs")
	}

	goodHostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, res, err := websocket.DefaultDialer.Dial(cleanHostURL, goodHostHeader)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected successful host WSS upgrade via Bearer header, got err: %v", err)
	}
	defer hostConn.Close()

	goodClientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, res, err := websocket.DefaultDialer.Dial(cleanClientURL, goodClientHeader)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected successful client WSS upgrade via Bearer header, got err: %v", err)
	}
	defer clientConn.Close()

	// Server sends explicit session_ready frame to client upon connection
	var ackFrame FrameEnvelope
	if err := clientConn.ReadJSON(&ackFrame); err != nil {
		t.Fatalf("Failed to read session_ready ack frame: %v", err)
	}
	if ackFrame.Type != "session_ready" {
		t.Fatalf("Expected session_ready ack frame, got: %s", ackFrame.Type)
	}

	// 6. Test Bidirectional Opaque Payload Frame Relaying
	time.Sleep(30 * time.Millisecond)
	opaquePayload := "sec_payload_e2e_verified_opaque_data"
	clientFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
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
		t.Errorf("Payload mismatch! Expected '%s', got '%s'", opaquePayload, hostFrame.EncryptedData)
	}

	// 7. Test Device Revocation Teardown
	b.RevokeDevice(machineID, deviceID)
	time.Sleep(30 * time.Millisecond)

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected active sessions to be 0 after revocation")
	}

	// Reconnect attempt for revoked device must fail
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, goodClientHeader)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for revoked device reconnect attempt, got: %v", res)
	}
}

func TestBroker_SubprotocolTokensRejected(t *testing.T) {
	b := NewBroker()
	machineID := "mach-subproto-01"
	deviceID := "dev-subproto-01"
	sessionID := "sess-subproto-01"
	hostToken := "host-token-subproto-32b"
	clientToken := "client-token-subproto-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// 1. Attempting to dial with Sec-WebSocket-Protocol carrying token must FAIL (HTTP 401 Unauthorized)
	hostDialer := websocket.Dialer{
		Subprotocols: []string{"devorbit-token." + hostToken},
	}
	cleanHostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	_, res, err := hostDialer.Dial(cleanHostURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 Unauthorized when attempting auth via subprotocol token, got status: %v, err: %v", res, err)
	}

	clientDialer := websocket.Dialer{
		Subprotocols: []string{"devorbit-token." + clientToken},
	}
	cleanClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	_, res, err = clientDialer.Dial(cleanClientURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 Unauthorized when client attempts auth via subprotocol token, got status: %v, err: %v", res, err)
	}

	// 2. Dialing with standard Authorization: Bearer header succeeds cleanly
	authHeaders := http.Header{
		"Authorization": []string{"Bearer " + hostToken},
	}
	hostConn, res, err := websocket.DefaultDialer.Dial(cleanHostURL, authHeaders)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected successful host upgrade via Authorization Bearer header, got err: %v", err)
	}
	defer hostConn.Close()

	clientAuthHeaders := http.Header{
		"Authorization": []string{"Bearer " + clientToken},
	}
	clientConn, res, err := websocket.DefaultDialer.Dial(cleanClientURL, clientAuthHeaders)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected successful client upgrade via Authorization Bearer header, got err: %v", err)
	}
	defer clientConn.Close()
}

func TestBroker_NativeAndBrowserOriginPolicy(t *testing.T) {
	b := NewBroker()
	machineID := "mach-origin-01"
	deviceID := "dev-origin-01"
	sessionID := "sess-origin-01"
	token := "valid-token-32b"

	b.AuthorizeDevice(machineID, deviceID, sessionID, token)
	b.AddAllowedOrigin("https://app.devorbit.net")
	b.AddAllowedOrigin("http://localhost:8081")

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	cleanClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)

	// 1. Native Client (Missing Origin Header) with valid credentials -> Succeeds
	nativeHeaders := http.Header{
		"Authorization": []string{"Bearer " + token},
	}
	conn, res, err := websocket.DefaultDialer.Dial(cleanClientURL, nativeHeaders)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected native client without Origin header to succeed with valid credentials, got: %v", err)
	}
	conn.Close()

	// 2. Native Client (Missing Origin Header) with invalid credentials -> Fails (403)
	badNativeHeaders := http.Header{
		"Authorization": []string{"Bearer invalid-token"},
	}
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, badNativeHeaders)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected invalid credentials to fail for native client, got: %v", res)
	}

	// 3. Allowed Browser Origin with valid credentials -> Succeeds
	allowedBrowserHeaders := http.Header{
		"Authorization": []string{"Bearer " + token},
		"Origin":        []string{"https://app.devorbit.net"},
	}
	conn, res, err = websocket.DefaultDialer.Dial(cleanClientURL, allowedBrowserHeaders)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected allowed browser origin to succeed, got: %v", err)
	}
	conn.Close()

	// 4. Unapproved Browser Origin -> Fails with HTTP 403 Forbidden
	unapprovedBrowserHeaders := http.Header{
		"Authorization": []string{"Bearer " + token},
		"Origin":        []string{"https://malicious-phishing-site.com"},
	}
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, unapprovedBrowserHeaders)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for unapproved browser origin, got: %v", res)
	}

	// 5. Spoofed User-Agent with Unapproved Origin -> Fails with HTTP 403 Forbidden
	spoofedHeaders := http.Header{
		"Authorization": []string{"Bearer " + token},
		"Origin":        []string{"https://malicious-phishing-site.com"},
		"User-Agent":    []string{"DevOrbit-Mobile-App-Android/1.2.1"},
	}
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, spoofedHeaders)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for spoofed User-Agent with unapproved Origin, got: %v", res)
	}

	// 6. Browser Origin with Wrong Port or Scheme -> Fails with HTTP 403 Forbidden
	wrongPortHeaders := http.Header{
		"Authorization": []string{"Bearer " + token},
		"Origin":        []string{"http://localhost:9999"},
	}
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, wrongPortHeaders)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for origin with unapproved port, got: %v", res)
	}
}

func TestBroker_ExpiredSessionRejection(t *testing.T) {
	b := NewBroker()
	machineID := "mach-exp-01"
	deviceID := "dev-exp-01"
	sessionID := "sess-exp-01"
	hostToken := "host-token-32b"
	clientToken := "client-token-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	// Pre-seed an expired session
	b.sessions[sessionID] = &Session{
		ID:        sessionID,
		MachineID: machineID,
		DeviceID:  deviceID,
		Token:     clientToken,
		ExpiresAt: time.Now().Add(-5 * time.Minute), // Already expired
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	cleanClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)

	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	_, res, err := websocket.DefaultDialer.Dial(cleanClientURL, clientHeader)
	if err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected HTTP 401 Unauthorized for expired session attempt, got: %v", res)
	}
}

func TestBroker_ServiceToServiceAuthorizationAndRevocation(t *testing.T) {
	adminSecret := "test-broker-service-secret-32b!"
	b := NewBrokerWithSecret(adminSecret)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/internal/sessions/authorize", b.HandleAuthorizeSession)
	mux.HandleFunc("/api/v1/internal/devices/revoke", b.HandleRevoke)
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	authURL := server.URL + "/api/v1/internal/sessions/authorize"
	revokeURL := server.URL + "/api/v1/internal/devices/revoke"

	machineID := "mach-srv-01"
	deviceID := "dev-srv-01"
	sessionID := "sess-srv-01"
	clientToken := "client-srv-token-32b"
	hostToken := "host-srv-token-32b"

	reqBody := fmt.Sprintf(`{
		"machine_id": "%s",
		"device_id": "%s",
		"session_id": "%s",
		"client_token": "%s",
		"host_token": "%s",
		"ttl_seconds": 3600
	}`, machineID, deviceID, sessionID, clientToken, hostToken)

	// 1. Missing service auth -> 401
	res, err := http.Post(authURL, "application/json", strings.NewReader(reqBody))
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 for missing service auth, got: %v", res)
	}

	// 2. Wrong service auth -> 401
	req, _ := http.NewRequest(http.MethodPost, authURL, strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer wrong-secret")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 for wrong service auth, got: %v", res)
	}

	// 3. Valid service auth -> 200 OK
	req, _ = http.NewRequest(http.MethodPost, authURL, strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 for valid service auth, got: %v", res)
	}

	// 4. Verify host and client can connect with registered credentials
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	hostDialURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, resHost, err := websocket.DefaultDialer.Dial(hostDialURL, hostHeader)
	if err != nil || resHost.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected host to connect successfully: %v", err)
	}
	defer hostConn.Close()

	clientDialURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, resClient, err := websocket.DefaultDialer.Dial(clientDialURL, clientHeader)
	if err != nil || resClient.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected client to connect successfully: %v", err)
	}
	defer clientConn.Close()

	// Client receives session_ready frame
	var ack FrameEnvelope
	if err := clientConn.ReadJSON(&ack); err != nil || ack.Type != "session_ready" {
		t.Fatalf("Expected session_ready ack frame, got: %v, err: %v", ack, err)
	}

	// 5. Revoke device via service-to-service API
	revokeBody := fmt.Sprintf(`{"machine_id":"%s","device_id":"%s"}`, machineID, deviceID)
	reqRevoke, _ := http.NewRequest(http.MethodPost, revokeURL, strings.NewReader(revokeBody))
	reqRevoke.Header.Set("Authorization", "Bearer "+adminSecret)
	reqRevoke.Header.Set("Content-Type", "application/json")
	resRev, err := http.DefaultClient.Do(reqRevoke)
	if err != nil || resRev.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 for device revocation, got: %v", resRev)
	}

	// 6. Subsequent client connection attempt MUST be rejected (403 Forbidden)
	_, resBlocked, err := websocket.DefaultDialer.Dial(clientDialURL, clientHeader)
	if err == nil || resBlocked == nil || resBlocked.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected HTTP 403 Forbidden after device revocation, got: %v", resBlocked)
	}
}

func TestBroker_ConsistentSessionExpiryAndReconnect(t *testing.T) {
	adminSecret := "broker-admin-secret-32b-verified!"
	b := NewBrokerWithSecret(adminSecret)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/internal/sessions/authorize", b.HandleAuthorizeSession)
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	machineID := "mach-exp-01"
	deviceID := "dev-exp-01"
	sessionID := "sess-exp-01"
	clientToken := "client-token-exp-32b"
	hostToken := "host-token-exp-32b"

	// 1. Authorize with short TTL (10s)
	authBody := fmt.Sprintf(`{
		"machine_id": "%s",
		"device_id": "%s",
		"session_id": "%s",
		"client_token": "%s",
		"host_token": "%s",
		"ttl_seconds": 10
	}`, machineID, deviceID, sessionID, clientToken, hostToken)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/internal/sessions/authorize", strings.NewReader(authBody))
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to authorize session: %v, resp: %v", err, resp)
	}

	// 2. Connect Host
	hostDialURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeader := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, err := websocket.DefaultDialer.Dial(hostDialURL, hostHeader)
	if err != nil {
		t.Fatalf("Failed to connect host: %v", err)
	}
	defer hostConn.Close()

	// 3. Connect Client
	clientDialURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeader := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, err := websocket.DefaultDialer.Dial(clientDialURL, clientHeader)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}
	var ack FrameEnvelope
	_ = clientConn.ReadJSON(&ack)
	clientConn.Close()

	// 4. Reconnect host: verify it does not reset or extend expiry
	hostConn2, _, err := websocket.DefaultDialer.Dial(hostDialURL, hostHeader)
	if err != nil {
		t.Fatalf("Host reconnect failed: %v", err)
	}
	defer hostConn2.Close()

	// Verify session in broker reflects the authoritative 10s expiry
	b.mu.RLock()
	sess := b.sessions[sessionID]
	b.mu.RUnlock()
	if sess == nil {
		t.Fatalf("Expected active session")
	}
	if time.Until(sess.ExpiresAt) > 11*time.Second {
		t.Fatalf("Expected expiry to be bounded to ~10s, got remaining: %v", time.Until(sess.ExpiresAt))
	}
}

func TestBroker_BoundedTTLAndIdentityHardening(t *testing.T) {
	adminSecret := "broker-admin-secret-32b-verified!"
	b := NewBrokerWithSecret(adminSecret)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/internal/sessions/authorize", b.HandleAuthorizeSession)

	server := httptest.NewServer(mux)
	defer server.Close()

	authURL := server.URL + "/api/v1/internal/sessions/authorize"

	// 1. Invalid TTL (< 10s) must be rejected
	bodyTooShort := `{"machine_id":"m1","device_id":"d1","session_id":"s1","client_token":"t1","ttl_seconds":5}`
	req, _ := http.NewRequest(http.MethodPost, authURL, strings.NewReader(bodyTooShort))
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP 400 for ttl < 10s, got %d", resp.StatusCode)
	}

	// 2. Register valid host
	bodyValid := `{"machine_id":"m1","device_id":"d1","session_id":"s1","client_token":"t1","host_token":"h-token-original","ttl_seconds":3600}`
	req, _ = http.NewRequest(http.MethodPost, authURL, strings.NewReader(bodyValid))
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 for valid registration, got %d", resp.StatusCode)
	}

	// 3. Attempt to hijack machine_id with a different host token must fail with 409 Conflict
	bodyHijack := `{"machine_id":"m1","device_id":"d2","session_id":"s2","client_token":"t2","host_token":"h-token-attacker","ttl_seconds":3600}`
	req, _ = http.NewRequest(http.MethodPost, authURL, strings.NewReader(bodyHijack))
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("Expected HTTP 409 Conflict for host token replacement attempt, got %d", resp.StatusCode)
	}

	// 4. Attempt to register same session_id with different client_token must fail with 409 Conflict
	bodyConflict := `{"machine_id":"m1","device_id":"d1","session_id":"s1","client_token":"different-token","ttl_seconds":3600}`
	req, _ = http.NewRequest(http.MethodPost, authURL, strings.NewReader(bodyConflict))
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("Expected HTTP 409 Conflict for conflicting client token on existing session, got %d", resp.StatusCode)
	}
}

func TestBroker_ConcurrentRelayAndLifecycleStress(t *testing.T) {
	b := NewBroker()
	machineID := "mach-stress-01"
	deviceID := "dev-stress-01"
	sessionID := "sess-stress-01"
	hostToken := "host-stress-token-32b"
	clientToken := "client-stress-token-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	cleanHostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	cleanClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)

	hostConn, _, err := websocket.DefaultDialer.Dial(cleanHostURL, http.Header{"Authorization": []string{"Bearer " + hostToken}})
	if err != nil {
		t.Fatalf("Host connection failed: %v", err)
	}
	defer hostConn.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial(cleanClientURL, http.Header{"Authorization": []string{"Bearer " + clientToken}})
	if err != nil {
		t.Fatalf("Client connection failed: %v", err)
	}
	defer clientConn.Close()

	// Drain session_ready frame
	var ackFrame FrameEnvelope
	_ = clientConn.ReadJSON(&ackFrame)

	// Launch concurrent frame relays while triggering revocation/disconnections
	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 50; j++ {
				frame := &FrameEnvelope{
					ProtocolVersion: "1.2",
					SessionID:       sessionID,
					Type:            "data",
					Timestamp:       time.Now().UnixMicro(),
				}
				_ = b.RelayFrameFromClientToHost(sessionID, frame)
				_ = b.RelayFrameFromHostToClient(sessionID, frame)
			}
			done <- true
		}(i)
	}

	// Trigger concurrent revocation and registration actions
	go func() {
		time.Sleep(5 * time.Millisecond)
		b.RevokeDevice(machineID, deviceID)
	}()

	for i := 0; i < 5; i++ {
		<-done
	}
}
