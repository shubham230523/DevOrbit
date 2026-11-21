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
	"devorbit.net/services/connection-broker/pkg/broker"
	"github.com/gorilla/websocket"
)

func TestDevOrbit_FullPairingAndAuthenticatedSessionLifecycle(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_pairing_e2e_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	// -------------------------------------------------------------
	// 1. Start Production Connection Broker with Service Auth
	// -------------------------------------------------------------
	adminSecret := "test-broker-service-admin-secret-32b!"
	realBroker := broker.NewBrokerWithSecret(adminSecret)

	brokerMux := http.NewServeMux()
	brokerMux.HandleFunc("/api/v1/internal/sessions/authorize", realBroker.HandleAuthorizeSession)
	brokerMux.HandleFunc("/api/v1/internal/devices/revoke", realBroker.HandleRevoke)
	brokerMux.HandleFunc("/ws/host", realBroker.HandleHostWebSocket)
	brokerMux.HandleFunc("/ws/client", realBroker.HandleClientWebSocket)

	brokerServer := httptest.NewServer(brokerMux)
	defer brokerServer.Close()
	brokerWSURL := strings.Replace(brokerServer.URL, "http://", "ws://", 1)

	// -------------------------------------------------------------
	// 2. Start Production Host Bridge with Real Broker Authorizer
	// -------------------------------------------------------------
	machineID := "host-windows-dev-01"
	hostSecret := "devorbit-host-wide-secret-key-32b!"
	pairingEngine := security.NewPairingEngine(machineID, "Dev Workstation", hostSecret)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	policy := security.NewDefaultCommandPolicy()
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
	serverEngine.SetBrokerURL(brokerWSURL)
	serverEngine.SetHostToken(hostSecret)

	// Wire real HTTP broker authorizer
	brokerAuthorizer := transport.NewHTTPBrokerAuthorizer(brokerServer.URL, adminSecret, nil)
	serverEngine.SetBrokerAuthorizer(brokerAuthorizer)

	hostMux := http.NewServeMux()
	serverEngine.RegisterRoutes(hostMux)
	hostHTTPServer := httptest.NewServer(hostMux)
	defer hostHTTPServer.Close()
	serverEngine.SetAdvertisedURL(hostHTTPServer.URL)

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
	// 3. Request a QR challenge from the real Host Bridge endpoint
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
		BrokerURL      string `json:"broker_url"`
		ExpiresAt      string `json:"expires_at"`
	}
	if err := json.NewDecoder(resCh.Body).Decode(&qrPayload); err != nil {
		t.Fatalf("Failed to decode QR challenge response: %v", err)
	}

	// Verify exact emitted payload contains valid absolute confirmation URL and ephemeral secret
	if !strings.HasPrefix(qrPayload.ConfirmURL, "http://") && !strings.HasPrefix(qrPayload.ConfirmURL, "https://") {
		t.Fatalf("Expected confirm_url to be absolute URL, got: %s", qrPayload.ConfirmURL)
	}
	if qrPayload.PairingSecret == "" || qrPayload.PairingSecret == hostSecret {
		t.Fatalf("Ephemeral pairing secret must be distinct non-empty random secret")
	}

	// -------------------------------------------------------------
	// 4. Verify Long-Lived Secret Rejection
	// -------------------------------------------------------------
	deviceID := "mobile-pixel-99"
	badPersistentProof := security.GenerateProof(
		qrPayload.Version,
		qrPayload.ChallengeID,
		qrPayload.PairingPayload,
		qrPayload.HostID,
		deviceID,
		qrPayload.ExpiresAt,
		hostSecret, // Wrong! Must use ephemeral secret
	)

	badReqBody, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload.ChallengeID,
		"device_id":         deviceID,
		"device_name":       "Pixel 9 Pro",
		"device_public_key": "pubkey-mobile-01",
		"proof_signature":   badPersistentProof,
	})
	resBad, err := http.Post(qrPayload.ConfirmURL, "application/json", bytes.NewBuffer(badReqBody))
	if err != nil || resBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP 400 when confirming with persistent host secret, got: %v", resBad)
	}

	// -------------------------------------------------------------
	// 5. Confirm Pairing using Ephemeral Proof -> Triggers Broker Auth & Session Provisioning
	// -------------------------------------------------------------
	validProof := security.GenerateProof(
		qrPayload.Version,
		qrPayload.ChallengeID,
		qrPayload.PairingPayload,
		qrPayload.HostID,
		deviceID,
		qrPayload.ExpiresAt,
		qrPayload.PairingSecret,
	)

	confirmReqBody, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload.ChallengeID,
		"device_id":         deviceID,
		"device_name":       "Pixel 9 Pro",
		"device_public_key": "pubkey-mobile-01",
		"proof_signature":   validProof,
	})

	resConf, err := http.Post(qrPayload.ConfirmURL, "application/json", bytes.NewBuffer(confirmReqBody))
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
		BrokerURL     string `json:"broker_url"`
		ExpiresAt     string `json:"expires_at"`
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

	// -------------------------------------------------------------
	// 6. Verify Host Bridge Session Provisioning Occurred
	// -------------------------------------------------------------
	provisionedSessionID, provisionedE2E := brokerClient.ActiveSession()
	if provisionedSessionID != confResp.SessionID || provisionedE2E == nil {
		t.Fatalf("Host Bridge did not receive session provisioning: expected %s, got %s", confResp.SessionID, provisionedSessionID)
	}

	// Wait for Host Bridge to connect to Broker
	time.Sleep(150 * time.Millisecond)

	// -------------------------------------------------------------
	// 7. Connect Mobile-Side Test Client using Issued Authorization Bearer Token
	// -------------------------------------------------------------
	// Mobile connects to real Connection Broker WITHOUT ANY MANUAL AUTHORIZATION INSERTS
	clientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", brokerWSURL, machineID, deviceID, confResp.SessionID)
	clientHeaders := http.Header{
		"Authorization": []string{"Bearer " + confResp.SessionToken},
	}
	clientWS, resWS, err := websocket.DefaultDialer.Dial(clientURL, clientHeaders)
	if err != nil || resWS.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Mobile failed to connect to broker WSS via production authorization: %v", err)
	}
	defer clientWS.Close()

	// Server sends explicit session_ready frame to confirm authentication
	var ackFrame broker.FrameEnvelope
	if err := clientWS.ReadJSON(&ackFrame); err != nil || ackFrame.Type != "session_ready" {
		t.Fatalf("Expected session_ready ack frame from broker, got: %v, err: %v", ackFrame, err)
	}

	// -------------------------------------------------------------
	// 8. Mobile Sends E2E Encrypted Request through Actual Broker Relay
	// -------------------------------------------------------------
	clientE2E, err := security.NewE2ESession(confResp.SessionID, deviceID, confResp.SessionSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to initialize mobile client E2E session: %v", err)
	}

	reqPayload := `{"v":"1.2","request_id":"req-e2e-001","method":"GET","path":"/api/v1/health"}`
	seqNum := int64(1)
	timestamp := time.Now().UnixMicro()

	cipherHex, err := clientE2E.Encrypt(security.DirClientToHost, []byte(reqPayload), seqNum, timestamp)
	if err != nil {
		t.Fatalf("Failed to encrypt client request: %v", err)
	}

	clientFrame := broker.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       confResp.SessionID,
		Type:            "data",
		EncryptedData:   cipherHex,
		SeqNum:          seqNum,
		Timestamp:       timestamp,
	}

	if err := clientWS.WriteJSON(&clientFrame); err != nil {
		t.Fatalf("Failed to send frame to broker: %v", err)
	}

	// -------------------------------------------------------------
	// 9 & 10. Verify Host Bridge Processes Request and Client Decrypts Response
	// -------------------------------------------------------------
	var respFrame broker.FrameEnvelope
	if err := clientWS.ReadJSON(&respFrame); err != nil {
		t.Fatalf("Failed to read response from broker: %v", err)
	}

	if respFrame.Type != "data" || respFrame.EncryptedData == "" {
		t.Fatalf("Expected encrypted data response frame, got: %+v", respFrame)
	}

	decryptedBytes, err := clientE2E.Decrypt(security.DirHostToClient, respFrame.EncryptedData, respFrame.SeqNum, respFrame.Timestamp)
	if err != nil {
		t.Fatalf("Client failed to decrypt relayed response: %v", err)
	}

	var appResp struct {
		Version    string `json:"v"`
		RequestID  string `json:"request_id"`
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(decryptedBytes, &appResp); err != nil {
		t.Fatalf("Failed to parse application response: %v, raw: %s", err, string(decryptedBytes))
	}

	if appResp.RequestID != "req-e2e-001" || appResp.StatusCode != http.StatusOK {
		t.Fatalf("Unexpected application response: %+v", appResp)
	}

	// -------------------------------------------------------------
	// 11. Revoke Paired Device through Real Revocation Path
	// -------------------------------------------------------------
	err = brokerAuthorizer.RevokeDevice(context.Background(), machineID, deviceID)
	if err != nil {
		t.Fatalf("Failed to revoke device: %v", err)
	}
	brokerClient.RevokeSession(confResp.SessionID)

	time.Sleep(50 * time.Millisecond)

	// -------------------------------------------------------------
	// 12. Subsequent Connection Attempt Rejected
	// -------------------------------------------------------------
	_, resBlocked, err := websocket.DefaultDialer.Dial(clientURL, clientHeaders)
	if err == nil || resBlocked == nil || resBlocked.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected HTTP 403 Forbidden after device revocation, got: %v", resBlocked)
	}

	// -------------------------------------------------------------
	// 13. Fresh Pairing Creates a New Independent Session
	// -------------------------------------------------------------
	resCh2, err := http.Post(hostHTTPServer.URL+"/api/v1/pairing/challenge", "application/json", nil)
	if err != nil || resCh2.StatusCode != http.StatusOK {
		t.Fatalf("Failed to create fresh pairing challenge: %v", err)
	}
	var qrPayload2 struct {
		Version        string `json:"v"`
		ChallengeID    string `json:"challenge_id"`
		HostID         string `json:"host_id"`
		PairingPayload string `json:"pairing_payload"`
		PairingSecret  string `json:"pairing_secret"`
		ConfirmURL     string `json:"confirm_url"`
		ExpiresAt      string `json:"expires_at"`
	}
	_ = json.NewDecoder(resCh2.Body).Decode(&qrPayload2)

	deviceID2 := "mobile-pixel-99-new"
	proof2 := security.GenerateProof(
		qrPayload2.Version,
		qrPayload2.ChallengeID,
		qrPayload2.PairingPayload,
		qrPayload2.HostID,
		deviceID2,
		qrPayload2.ExpiresAt,
		qrPayload2.PairingSecret,
	)

	confirmReq2, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload2.ChallengeID,
		"device_id":         deviceID2,
		"device_name":       "Pixel 9 Pro II",
		"device_public_key": "pubkey-mobile-02",
		"proof_signature":   proof2,
	})

	resConf2, err := http.Post(qrPayload2.ConfirmURL, "application/json", bytes.NewBuffer(confirmReq2))
	if err != nil || resConf2.StatusCode != http.StatusOK {
		t.Fatalf("Fresh pairing confirmation failed: %v", resConf2)
	}

	var confResp2 struct {
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
	}
	_ = json.NewDecoder(resConf2.Body).Decode(&confResp2)

	if confResp2.SessionID == confResp.SessionID {
		t.Fatalf("Fresh pairing must produce distinct session ID")
	}

	newSessionID, newE2E := brokerClient.ActiveSession()
	if newSessionID != confResp2.SessionID || newE2E == nil {
		t.Fatalf("Fresh session was not provisioned properly on host bridge")
	}
}
