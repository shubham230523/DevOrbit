package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
	"devorbit.net/agents/host-bridge/internal/transport"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
	"github.com/gorilla/websocket"
)

var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func TestDevOrbit_FullPairingAndAuthenticatedSessionLifecycle(t *testing.T) {
	// -------------------------------------------------------------
	// 0. Setup Infrastructure & Real WebSocket Relay Broker
	// -------------------------------------------------------------
	tempRoot, err := os.MkdirTemp("", "devorbit_pairing_e2e_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	var mu sync.Mutex
	var activeHostConn *websocket.Conn
	var activeClientConn *websocket.Conn
	authorizedTokens := make(map[string]string)
	revokedSessions := make(map[string]bool)

	brokerMux := http.NewServeMux()
	brokerMux.HandleFunc("/ws/host", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized: missing Bearer header", http.StatusUnauthorized)
			return
		}
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		activeHostConn = conn
		mu.Unlock()

		defer conn.Close()
		for {
			var msg json.RawMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}
			mu.Lock()
			cl := activeClientConn
			mu.Unlock()
			if cl != nil {
				_ = cl.WriteJSON(msg)
			}
		}
	})

	brokerMux.HandleFunc("/ws/client", func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session_id")
		deviceID := r.URL.Query().Get("device_id")
		mu.Lock()
		isRevoked := revokedSessions[sessionID]
		expectedToken := authorizedTokens[sessionID+":"+deviceID]
		mu.Unlock()

		if isRevoked {
			http.Error(w, "Forbidden: revoked device or session", http.StatusForbidden)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized: missing Bearer header", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != expectedToken {
			http.Error(w, "Unauthorized: invalid client token", http.StatusUnauthorized)
			return
		}

		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		activeClientConn = conn
		mu.Unlock()

		defer conn.Close()
		for {
			var msg json.RawMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}
			mu.Lock()
			h := activeHostConn
			mu.Unlock()
			if h != nil {
				_ = h.WriteJSON(msg)
			}
		}
	})

	brokerServer := httptest.NewServer(brokerMux)
	defer brokerServer.Close()
	brokerWSURL := strings.Replace(brokerServer.URL, "http://", "ws://", 1)

	// Setup Host Bridge Components
	machineID := "host-workstation-e2e-01"
	hostName := "Desktop Workstation"
	hostSecret := "host-master-secret-key-32-bytes!"

	jail := security.NewWorkspaceJail([]string{tempRoot})
	policy := security.NewDefaultCommandPolicy()
	pairingEngine := security.NewPairingEngine(machineID, hostName, hostSecret)

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storage)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	serverEngine := httptransport.NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	serverEngine.SetPairingEngine(pairingEngine)

	hostMux := http.NewServeMux()
	serverEngine.RegisterRoutes(hostMux)
	hostHTTPServer := httptest.NewServer(hostMux)
	defer hostHTTPServer.Close()

	// Outbound broker client starts disconnected
	brokerClient := transport.NewBrokerClient(brokerWSURL, machineID, "", hostSecret, serverEngine)
	serverEngine.SetSessionProvisioner(func(sessionID, deviceID, sessionSecret string) error {
		return brokerClient.ProvisionSession(sessionID, deviceID, sessionSecret, 24*time.Hour)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	brokerClient.Start(ctx)
	defer brokerClient.Stop()

	// -------------------------------------------------------------
	// 1. Host creates a pairing challenge (POST /api/v1/pairing/challenge)
	// -------------------------------------------------------------
	resCh, err := http.Post(hostHTTPServer.URL+"/api/v1/pairing/challenge", "application/json", nil)
	if err != nil || resCh.StatusCode != http.StatusOK {
		t.Fatalf("Failed to create pairing challenge: %v", err)
	}

	var qrPayload struct {
		Version        string `json:"v"`
		ChallengeID    string `json:"challenge_id"`
		HostID         string `json:"host_id"`
		HostName       string `json:"host_name"`
		PairingPayload string `json:"pairing_payload"`
		PairingSecret  string `json:"pairing_secret"`
		ConfirmURL     string `json:"confirm_url"`
		ExpiresAt      string `json:"expires_at"`
	}
	if err := json.NewDecoder(resCh.Body).Decode(&qrPayload); err != nil {
		t.Fatalf("Failed to parse QR payload: %v", err)
	}

	// 2. Mobile obtains and scans a real-format QR payload
	if qrPayload.Version != "1.0" || qrPayload.ChallengeID == "" || qrPayload.PairingPayload == "" || qrPayload.PairingSecret == "" {
		t.Fatalf("Invalid QR payload structure: %+v", qrPayload)
	}
	if qrPayload.PairingSecret == hostSecret {
		t.Fatalf("Security failure: Ephemeral challenge secret must NOT be host secret")
	}

	// 3. Test Expired Challenge Rejection
	expiredCh, err := pairingEngine.CreateChallenge(-1 * time.Second)
	if err != nil {
		t.Fatalf("Failed to create expired challenge: %v", err)
	}
	expiredProof := pairingEngine.GenerateProof(expiredCh.Payload, "mobile-phone-01")
	expiredReqBody, _ := json.Marshal(map[string]string{
		"challenge_id":    expiredCh.ID,
		"device_id":       "mobile-phone-01",
		"proof_signature": expiredProof,
	})
	resExp, err := http.Post(hostHTTPServer.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(expiredReqBody))
	if err != nil || resExp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP 400 for expired challenge, got: %v", resExp)
	}

	// 4. Mobile computes proof of possession
	deviceID := "mobile-pixel-99"
	mobileProof := pairingEngine.GenerateProof(qrPayload.PairingPayload, deviceID)

	// 5. Successful Pairing and Credential Issuance
	confirmReqBody, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload.ChallengeID,
		"device_id":         deviceID,
		"device_name":       "Pixel 9 Pro",
		"device_public_key": "pubkey-mobile-01",
		"proof_signature":   mobileProof,
	})
	resConf, err := http.Post(hostHTTPServer.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(confirmReqBody))
	if err != nil || resConf.StatusCode != http.StatusOK {
		t.Fatalf("Pairing confirmation failed: %v", resConf)
	}

	var confResp struct {
		Status        string `json:"status"`
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
		MachineID     string `json:"machine_id"`
		DeviceID      string `json:"device_id"`
	}
	if err := json.NewDecoder(resConf.Body).Decode(&confResp); err != nil {
		t.Fatalf("Failed to parse pairing confirmation response: %v", err)
	}

	if confResp.Status != "success" || confResp.SessionToken == "" || confResp.SessionID == "" || confResp.SessionSecret == "" {
		t.Fatalf("Invalid or missing session credentials: %+v", confResp)
	}
	if confResp.SessionSecret == hostSecret {
		t.Fatalf("Security failure: Provisioned session secret cannot be identical to host-wide secret")
	}

	// 6. Test Replayed Challenge Rejection
	resReplay, err := http.Post(hostHTTPServer.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(confirmReqBody))
	if err != nil || resReplay.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP 400 for replayed challenge, got: %v", resReplay)
	}

	// 7. Verify Host Bridge session provisioning occurred
	provisionedSessionID, provisionedE2E := brokerClient.ActiveSession()

	if provisionedSessionID != confResp.SessionID || provisionedE2E == nil {
		t.Fatalf("Host Bridge did not receive session provisioning: expected %s, got %s", confResp.SessionID, provisionedSessionID)
	}

	// 8. Authorize Client on Broker
	mu.Lock()
	authorizedTokens[confResp.SessionID+":"+deviceID] = confResp.SessionToken
	mu.Unlock()

	// Wait for Host Bridge to connect to Broker
	time.Sleep(150 * time.Millisecond)

	// 9. Mobile connects to Broker using Authorization Bearer header
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", brokerWSURL, machineID, deviceID, confResp.SessionID)
	clientHeaders := http.Header{
		"Authorization": []string{"Bearer " + confResp.SessionToken},
	}
	clientWS, resWS, err := websocket.DefaultDialer.Dial(clientURL, clientHeaders)
	if err != nil || resWS.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Mobile failed to connect to broker WSS: %v (response: %v)", err, resWS)
	}
	defer clientWS.Close()

	// 10. End-to-End Encrypted Request/Response Exchange
	mobileE2E, err := security.NewE2ESession(confResp.SessionID, deviceID, confResp.SessionSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to initialize mobile E2E session: %v", err)
	}

	appReq := transport.ApplicationRequestEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       confResp.SessionID,
		RequestID:       "req-e2e-001",
		Method:          "GET",
		Path:            "/api/v1/health",
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}
	reqBytes, _ := json.Marshal(appReq)
	cipherHex, err := mobileE2E.Encrypt(security.DirClientToHost, reqBytes, 1, appReq.Timestamp)
	if err != nil {
		t.Fatalf("Mobile E2E encryption failed: %v", err)
	}

	frameToSend := transport.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       confResp.SessionID,
		Type:            "data",
		EncryptedData:   cipherHex,
		SeqNum:          1,
		Timestamp:       appReq.Timestamp,
	}
	if err := clientWS.WriteJSON(&frameToSend); err != nil {
		t.Fatalf("Failed to write frame to broker: %v", err)
	}

	// Read Host response frame on Mobile
	var respFrame transport.FrameEnvelope
	if err := clientWS.ReadJSON(&respFrame); err != nil {
		t.Fatalf("Failed to read response frame from broker: %v", err)
	}

	decryptedRespBytes, err := mobileE2E.Decrypt(security.DirHostToClient, respFrame.EncryptedData, respFrame.SeqNum, respFrame.Timestamp)
	if err != nil {
		t.Fatalf("Mobile E2E response decryption failed: %v", err)
	}

	var appResp transport.ApplicationResponseEnvelope
	if err := json.Unmarshal(decryptedRespBytes, &appResp); err != nil {
		t.Fatalf("Failed to unmarshal application response envelope: %v", err)
	}

	if appResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 from host, got %d", appResp.StatusCode)
	}
	if !strings.Contains(appResp.Body, "healthy") && !strings.Contains(appResp.Body, "status") {
		t.Errorf("Unexpected health response body: %s", appResp.Body)
	}

	// 11. Test Device Revocation
	mu.Lock()
	revokedSessions[confResp.SessionID] = true
	mu.Unlock()
	brokerClient.RevokeSession(confResp.SessionID)

	time.Sleep(50 * time.Millisecond)

	// Reconnect attempt for revoked device must fail closed (HTTP 403 Forbidden)
	_, resRevoked, err := websocket.DefaultDialer.Dial(clientURL, clientHeaders)
	if err == nil || resRevoked == nil || resRevoked.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for revoked device, got: %v", resRevoked)
	}

	// 12. Re-pairing creates a fresh session with a fresh session secret
	resFreshCh, _ := http.Post(hostHTTPServer.URL+"/api/v1/pairing/challenge", "application/json", nil)
	var freshQR struct {
		ChallengeID    string `json:"challenge_id"`
		PairingPayload string `json:"pairing_payload"`
	}
	_ = json.NewDecoder(resFreshCh.Body).Decode(&freshQR)

	freshProof := pairingEngine.GenerateProof(freshQR.PairingPayload, deviceID)
	freshConfirmBody, _ := json.Marshal(map[string]string{
		"challenge_id":    freshQR.ChallengeID,
		"device_id":       deviceID,
		"proof_signature": freshProof,
	})
	resFreshConf, _ := http.Post(hostHTTPServer.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(freshConfirmBody))
	var freshConfResp struct {
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
	}
	_ = json.NewDecoder(resFreshConf.Body).Decode(&freshConfResp)

	if freshConfResp.SessionID == confResp.SessionID {
		t.Errorf("Expected new session ID for re-pairing, got identical session ID")
	}
	if freshConfResp.SessionSecret == confResp.SessionSecret {
		t.Errorf("Expected fresh session secret for re-pairing, got identical secret")
	}
}
