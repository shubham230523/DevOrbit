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
	"devorbit.net/agents/host-bridge/internal/config"
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
	brokerHTTPURL := brokerServer.URL
	brokerWSURL := strings.Replace(brokerServer.URL, "http://", "ws://", 1)

	// -------------------------------------------------------------
	// 2. Production Configuration & Wiring for Host Bridge
	// -------------------------------------------------------------
	machineID := "host-windows-dev-01"
	hostSecret := "devorbit-host-wide-secret-key-32b!"

	os.Setenv("DEVORBIT_ENV", "development")
	os.Setenv("DEVORBIT_HOST_ID", machineID)
	os.Setenv("DEVORBIT_HOST_SECRET", hostSecret)
	os.Setenv("DEVORBIT_BROKER_HTTP_URL", brokerHTTPURL)
	os.Setenv("DEVORBIT_BROKER_WS_URL", brokerWSURL)
	os.Setenv("DEVORBIT_BROKER_SECRET", adminSecret)
	defer os.Unsetenv("DEVORBIT_ENV")
	defer os.Unsetenv("DEVORBIT_HOST_ID")
	defer os.Unsetenv("DEVORBIT_HOST_SECRET")
	defer os.Unsetenv("DEVORBIT_BROKER_HTTP_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_WS_URL")
	defer os.Unsetenv("DEVORBIT_BROKER_SECRET")

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load production config: %v", err)
	}

	jail := security.NewWorkspaceJail([]string{tempRoot})
	policy := security.NewDefaultCommandPolicy()
	pairingEngine := security.NewPairingEngine(cfg.HostID, cfg.HostName, cfg.HostSecret)

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
	serverEngine.SetBrokerHTTPURL(cfg.BrokerHTTPURL)
	serverEngine.SetBrokerWSURL(cfg.BrokerWSURL)
	serverEngine.SetHostToken(cfg.HostSecret)

	// Wire HTTP broker authorizer exclusively to HTTP endpoint
	brokerAuthorizer := transport.NewHTTPBrokerAuthorizer(cfg.BrokerHTTPURL, cfg.BrokerSecret, nil)
	serverEngine.SetBrokerAuthorizer(brokerAuthorizer)

	hostMux := http.NewServeMux()
	serverEngine.RegisterRoutes(hostMux)

	hostHTTPServer := httptest.NewServer(hostMux)
	defer hostHTTPServer.Close()
	serverEngine.SetAdvertisedURL(hostHTTPServer.URL)

	// Outbound broker client connects exclusively to WebSocket endpoint
	brokerClient := transport.NewBrokerClient(cfg.BrokerWSURL, cfg.HostID, "", cfg.HostSecret, serverEngine)
	serverEngine.SetSessionProvisioner(func(sessionID, deviceID, sessionSecret string, expiry time.Time) error {
		return brokerClient.ProvisionSession(sessionID, deviceID, sessionSecret, expiry)
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

	if !strings.HasPrefix(qrPayload.ConfirmURL, "http://") && !strings.HasPrefix(qrPayload.ConfirmURL, "https://") {
		t.Fatalf("Expected confirm_url to be absolute URL, got: %s", qrPayload.ConfirmURL)
	}
	// Verify broker_url emitted to mobile is WebSocket URL, NOT HTTP URL!
	if !strings.HasPrefix(qrPayload.BrokerURL, "ws://") && !strings.HasPrefix(qrPayload.BrokerURL, "wss://") {
		t.Fatalf("Expected broker_url to be WebSocket URL, got: %s", qrPayload.BrokerURL)
	}

	// -------------------------------------------------------------
	// 4. Verify Persistent Secret Rejection
	// -------------------------------------------------------------
	deviceID1 := "mobile-pixel-01"
	badPersistentProof := security.GenerateProof(
		qrPayload.Version,
		qrPayload.ChallengeID,
		qrPayload.PairingPayload,
		qrPayload.HostID,
		deviceID1,
		qrPayload.ExpiresAt,
		hostSecret,
	)

	badReqBody, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload.ChallengeID,
		"device_id":         deviceID1,
		"device_name":       "Pixel Phone",
		"device_public_key": "pubkey-mobile-01",
		"proof_signature":   badPersistentProof,
	})
	resBad, err := http.Post(qrPayload.ConfirmURL, "application/json", bytes.NewBuffer(badReqBody))
	if err != nil || resBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP 400 when confirming with persistent host secret, got: %v", resBad)
	}

	// -------------------------------------------------------------
	// 5. Confirm Pairing for Device 1 with Ephemeral Proof
	// -------------------------------------------------------------
	validProof1 := security.GenerateProof(
		qrPayload.Version,
		qrPayload.ChallengeID,
		qrPayload.PairingPayload,
		qrPayload.HostID,
		deviceID1,
		qrPayload.ExpiresAt,
		qrPayload.PairingSecret,
	)

	confirmReqBody1, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload.ChallengeID,
		"device_id":         deviceID1,
		"device_name":       "Pixel Phone",
		"device_public_key": "pubkey-mobile-01",
		"proof_signature":   validProof1,
	})

	resConf1, err := http.Post(qrPayload.ConfirmURL, "application/json", bytes.NewBuffer(confirmReqBody1))
	if err != nil || resConf1.StatusCode != http.StatusOK {
		t.Fatalf("Pairing confirmation for Device 1 failed: %v", resConf1)
	}

	var confResp1 struct {
		Status        string `json:"status"`
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
		MachineID     string `json:"machine_id"`
		DeviceID      string `json:"device_id"`
		BrokerURL     string `json:"broker_url"`
		ExpiresAt     string `json:"expires_at"`
	}
	_ = json.NewDecoder(resConf1.Body).Decode(&confResp1)

	// -------------------------------------------------------------
	// 6. Connect Device 1 to Broker WebSocket
	// -------------------------------------------------------------
	time.Sleep(100 * time.Millisecond)
	clientURL1 := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", brokerWSURL, machineID, deviceID1, confResp1.SessionID)
	clientHeaders1 := http.Header{"Authorization": []string{"Bearer " + confResp1.SessionToken}}
	clientWS1, resWS1, err := websocket.DefaultDialer.Dial(clientURL1, clientHeaders1)
	if err != nil || resWS1.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Device 1 failed to connect to broker: %v", err)
	}
	defer clientWS1.Close()

	var ackFrame1 broker.FrameEnvelope
	if err := clientWS1.ReadJSON(&ackFrame1); err != nil || ackFrame1.Type != "session_ready" {
		t.Fatalf("Device 1 did not receive session_ready: %v, err: %v", ackFrame1, err)
	}

	// -------------------------------------------------------------
	// 7. Pair and Connect Device 2 Simultaneously (Multi-Device Verification)
	// -------------------------------------------------------------
	resCh2, err := http.Post(hostHTTPServer.URL+"/api/v1/pairing/challenge", "application/json", nil)
	if err != nil || resCh2.StatusCode != http.StatusOK {
		t.Fatalf("Failed to create challenge for Device 2: %v", err)
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

	deviceID2 := "mobile-tablet-02"
	validProof2 := security.GenerateProof(
		qrPayload2.Version,
		qrPayload2.ChallengeID,
		qrPayload2.PairingPayload,
		qrPayload2.HostID,
		deviceID2,
		qrPayload2.ExpiresAt,
		qrPayload2.PairingSecret,
	)

	confirmReqBody2, _ := json.Marshal(map[string]string{
		"challenge_id":      qrPayload2.ChallengeID,
		"device_id":         deviceID2,
		"device_name":       "Galaxy Tablet",
		"device_public_key": "pubkey-mobile-02",
		"proof_signature":   validProof2,
	})

	resConf2, err := http.Post(qrPayload2.ConfirmURL, "application/json", bytes.NewBuffer(confirmReqBody2))
	if err != nil || resConf2.StatusCode != http.StatusOK {
		t.Fatalf("Pairing confirmation for Device 2 failed: %v", resConf2)
	}

	var confResp2 struct {
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
		ExpiresAt     string `json:"expires_at"`
	}
	_ = json.NewDecoder(resConf2.Body).Decode(&confResp2)

	if confResp2.SessionID == confResp1.SessionID {
		t.Fatalf("Device 2 must have distinct session ID from Device 1")
	}

	// Verify Host Bridge now maintains 2 simultaneous active device sessions
	if brokerClient.ActiveSessionCount() != 2 {
		t.Fatalf("Expected 2 active sessions on Host Bridge, got %d", brokerClient.ActiveSessionCount())
	}

	// Connect Device 2 to Broker WebSocket
	time.Sleep(100 * time.Millisecond)
	clientURL2 := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", brokerWSURL, machineID, deviceID2, confResp2.SessionID)
	clientHeaders2 := http.Header{"Authorization": []string{"Bearer " + confResp2.SessionToken}}
	clientWS2, resWS2, err := websocket.DefaultDialer.Dial(clientURL2, clientHeaders2)
	if err != nil || resWS2.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Device 2 failed to connect to broker: %v", err)
	}
	defer clientWS2.Close()

	var ackFrame2 broker.FrameEnvelope
	if err := clientWS2.ReadJSON(&ackFrame2); err != nil || ackFrame2.Type != "session_ready" {
		t.Fatalf("Device 2 did not receive session_ready: %v, err: %v", ackFrame2, err)
	}

	// -------------------------------------------------------------
	// 8 & 9. Both Devices Exchange Independent Encrypted Requests Simultaneously
	// -------------------------------------------------------------
	clientE2E1, _ := security.NewE2ESession(confResp1.SessionID, deviceID1, confResp1.SessionSecret, 24*time.Hour)
	clientE2E2, _ := security.NewE2ESession(confResp2.SessionID, deviceID2, confResp2.SessionSecret, 24*time.Hour)

	// Device 1 sends request
	reqPayload1 := `{"v":"1.2","request_id":"req-dev1-001","method":"GET","path":"/api/v1/health"}`
	ts1 := time.Now().UnixMicro()
	cipher1, _ := clientE2E1.Encrypt(security.DirClientToHost, []byte(reqPayload1), 1, ts1)
	_ = clientWS1.WriteJSON(broker.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       confResp1.SessionID,
		Type:            "data",
		EncryptedData:   cipher1,
		SeqNum:          1,
		Timestamp:       ts1,
	})

	// Device 2 sends request
	reqPayload2 := `{"v":"1.2","request_id":"req-dev2-002","method":"GET","path":"/api/v1/health"}`
	ts2 := time.Now().UnixMicro()
	cipher2, _ := clientE2E2.Encrypt(security.DirClientToHost, []byte(reqPayload2), 1, ts2)
	_ = clientWS2.WriteJSON(broker.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       confResp2.SessionID,
		Type:            "data",
		EncryptedData:   cipher2,
		SeqNum:          1,
		Timestamp:       ts2,
	})

	// Device 1 receives and decrypts its response
	var respFrame1 broker.FrameEnvelope
	if err := clientWS1.ReadJSON(&respFrame1); err != nil {
		t.Fatalf("Device 1 failed to read response frame: %v", err)
	}
	decrypted1, err := clientE2E1.Decrypt(security.DirHostToClient, respFrame1.EncryptedData, respFrame1.SeqNum, respFrame1.Timestamp)
	if err != nil || !strings.Contains(string(decrypted1), "req-dev1-001") {
		t.Fatalf("Device 1 failed to decrypt correct response: %v, raw: %s", err, string(decrypted1))
	}

	// Device 2 receives and decrypts its response
	var respFrame2 broker.FrameEnvelope
	if err := clientWS2.ReadJSON(&respFrame2); err != nil {
		t.Fatalf("Device 2 failed to read response frame: %v", err)
	}
	decrypted2, err := clientE2E2.Decrypt(security.DirHostToClient, respFrame2.EncryptedData, respFrame2.SeqNum, respFrame2.Timestamp)
	if err != nil || !strings.Contains(string(decrypted2), "req-dev2-002") {
		t.Fatalf("Device 2 failed to decrypt correct response: %v, raw: %s", err, string(decrypted2))
	}

	// -------------------------------------------------------------
	// 10. Revoke Device 1 Only -> Device 2 Must Continue Working
	// -------------------------------------------------------------
	err = brokerAuthorizer.RevokeDevice(context.Background(), machineID, deviceID1)
	if err != nil {
		t.Fatalf("Failed to revoke Device 1: %v", err)
	}
	brokerClient.RevokeDevice(deviceID1)

	// Verify Device 1's reconnection is rejected (403)
	time.Sleep(50 * time.Millisecond)
	_, resBlocked1, _ := websocket.DefaultDialer.Dial(clientURL1, clientHeaders1)
	if resBlocked1 == nil || resBlocked1.StatusCode != http.StatusForbidden {
		t.Fatalf("Expected HTTP 403 for revoked Device 1, got %v", resBlocked1)
	}

	// Verify Device 2 STILL WORKS and can exchange encrypted frames!
	ts3 := time.Now().UnixMicro()
	cipher3, _ := clientE2E2.Encrypt(security.DirClientToHost, []byte(`{"v":"1.2","request_id":"req-dev2-003","method":"GET","path":"/api/v1/health"}`), 2, ts3)
	_ = clientWS2.WriteJSON(broker.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       confResp2.SessionID,
		Type:            "data",
		EncryptedData:   cipher3,
		SeqNum:          2,
		Timestamp:       ts3,
	})

	var respFrame3 broker.FrameEnvelope
	if err := clientWS2.ReadJSON(&respFrame3); err != nil {
		t.Fatalf("Device 2 failed to read response after Device 1 revocation: %v", err)
	}
	decrypted3, err := clientE2E2.Decrypt(security.DirHostToClient, respFrame3.EncryptedData, respFrame3.SeqNum, respFrame3.Timestamp)
	if err != nil || !strings.Contains(string(decrypted3), "req-dev2-003") {
		t.Fatalf("Device 2 failed to decrypt response after Device 1 revocation: %v", err)
	}
}
