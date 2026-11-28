package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestDevOrbit_FullProductionPairingSessionLifecycle(t *testing.T) {
	// 1. Setup temporary workstation workspace
	tempWorkspace, err := os.MkdirTemp("", "devorbit_pairing_e2e_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempWorkspace)

	cmdGit := exec.Command("git", "init")
	cmdGit.Dir = tempWorkspace
	_ = cmdGit.Run()

	manifestPath := filepath.Join(tempWorkspace, "app", "src", "main", "AndroidManifest.xml")
	_ = os.MkdirAll(filepath.Dir(manifestPath), 0755)
	_ = os.WriteFile(manifestPath, []byte("<manifest package=\"com.devorbit.test\"></manifest>"), 0644)

	buildGradlePath := filepath.Join(tempWorkspace, "build.gradle.kts")
	_ = os.WriteFile(buildGradlePath, []byte("// initial build.gradle.kts"), 0644)

	// 2. Start Real Connection Broker Server
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	realBroker := broker.NewBrokerWithSecret(cfg.BrokerSecret)
	brokerMux := http.NewServeMux()
	brokerMux.HandleFunc("/api/v1/internal/sessions/authorize", realBroker.HandleAuthorizeSession)
	brokerMux.HandleFunc("/api/v1/internal/devices/revoke", realBroker.HandleRevoke)
	brokerMux.HandleFunc("/ws/host", realBroker.HandleHostWebSocket)
	brokerMux.HandleFunc("/ws/client", realBroker.HandleClientWebSocket)

	brokerHTTPServer := httptest.NewServer(brokerMux)
	defer brokerHTTPServer.Close()

	brokerWSURL := strings.Replace(brokerHTTPServer.URL, "http://", "ws://", 1)
	cfg.BrokerHTTPURL = brokerHTTPServer.URL
	cfg.BrokerWSURL = brokerWSURL

	// 3. Host Bridge Infrastructure & Pairing Setup
	jail := security.NewWorkspaceJail([]string{tempWorkspace})
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
	serverEngine.SetBrokerEndpoints(cfg.BrokerHTTPURL, cfg.BrokerWSURL, cfg.HostSecret)

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
		e2eSess, err := security.NewE2ESession(sessionID, deviceID, sessionSecret, time.Until(expiry))
		if err != nil {
			return err
		}
		brokerClient.SessionID = sessionID
		brokerClient.SetE2ESession(e2eSess)
		brokerClient.Start(context.Background())
		return nil
	})
	defer brokerClient.Stop()

	// 4. Create Pairing Challenge via POST (host admin authenticated)
	chReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/pairing/challenge", hostHTTPServer.URL), nil)
	chReq.Header.Set("Authorization", "Bearer "+cfg.HostSecret)
	resp, err := http.DefaultClient.Do(chReq)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch pairing challenge: %v (code=%d)", err, resp.StatusCode)
	}
	defer resp.Body.Close()

	var chResp struct {
		ChallengeID    string `json:"challenge_id"`
		PairingPayload string `json:"pairing_payload"`
		PairingSecret  string `json:"pairing_secret"`
		ConfirmURL     string `json:"confirm_url"`
		BrokerURL      string `json:"broker_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chResp); err != nil {
		t.Fatalf("Failed to decode challenge response: %v", err)
	}

	if chResp.ConfirmURL != fmt.Sprintf("%s/api/v1/pairing/confirm", hostHTTPServer.URL) {
		t.Errorf("Unexpected confirm_url: %s", chResp.ConfirmURL)
	}

	// 5. Confirm Pairing as Device 1
	dev1ID := "dev-phone-01"
	dev1ProofSig, err := pairingEngine.GenerateProofForChallenge(chResp.ChallengeID, dev1ID)
	if err != nil {
		t.Fatalf("Failed to generate proof for device 1: %v", err)
	}

	confirmBody1 := map[string]string{
		"challenge_id":      chResp.ChallengeID,
		"device_id":         dev1ID,
		"device_name":       "Pixel 8 Pro",
		"device_public_key": "pubkey-android-01",
		"proof_signature":   dev1ProofSig,
	}

	confirmBytes1, _ := json.Marshal(confirmBody1)
	confirmResp1, err := http.Post(chResp.ConfirmURL, "application/json", bytes.NewReader(confirmBytes1))
	if err != nil || confirmResp1.StatusCode != http.StatusOK {
		t.Fatalf("Pairing confirmation failed for device 1: %v", err)
	}
	defer confirmResp1.Body.Close()

	var paired1 struct {
		Status        string `json:"status"`
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
		MachineID     string `json:"machine_id"`
		DeviceID      string `json:"device_id"`
		BrokerURL     string `json:"broker_url"`
	}
	if err := json.NewDecoder(confirmResp1.Body).Decode(&paired1); err != nil {
		t.Fatalf("Failed to decode pairing response: %v", err)
	}

	if paired1.Status != "success" || paired1.SessionToken == "" || paired1.SessionID == "" || paired1.SessionSecret == "" {
		t.Fatalf("Invalid pairing response data")
	}

	time.Sleep(100 * time.Millisecond)

	// 6. Connect Client 1 to Broker via WSS using Bearer Authorization Header
	client1WSSURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", brokerWSURL, paired1.MachineID, paired1.DeviceID, paired1.SessionID)
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	header1 := http.Header{}
	header1.Set("Authorization", "Bearer "+paired1.SessionToken)

	client1Conn, _, err := dialer.Dial(client1WSSURL, header1)
	if err != nil {
		t.Fatalf("Failed client 1 WSS connection: %v", err)
	}
	defer client1Conn.Close()

	// 7. Verify E2E Encrypted Application Request & Response over Broker
	client1E2E, err := security.NewE2ESession(paired1.SessionID, paired1.DeviceID, paired1.SessionSecret, 24*time.Hour)
	if err != nil {
		t.Fatalf("Failed to create client E2E session: %v", err)
	}

	appReq1 := transport.ApplicationRequestEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       paired1.SessionID,
		RequestID:       "req_e2e_001",
		Method:          "GET",
		Path:            "/api/v1/health",
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}
	appReqBytes1, _ := json.Marshal(appReq1)

	cipherReq1, err := client1E2E.Encrypt(security.DirClientToHost, appReqBytes1, 1, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("Failed to encrypt app request 1: %v", err)
	}

	frame1 := transport.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       paired1.SessionID,
		Type:            "data",
		EncryptedData:   cipherReq1,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := client1Conn.WriteJSON(&frame1); err != nil {
		t.Fatalf("Failed to write frame from client 1: %v", err)
	}

	var respFrame1 transport.FrameEnvelope
	if err := client1Conn.ReadJSON(&respFrame1); err != nil {
		t.Fatalf("Client 1 failed to read response frame: %v", err)
	}

	decryptedRespBytes1, err := client1E2E.Decrypt(security.DirHostToClient, respFrame1.EncryptedData, respFrame1.SeqNum, respFrame1.Timestamp)
	if err != nil {
		t.Fatalf("Client 1 failed to decrypt response frame: %v", err)
	}

	var appResp1 transport.ApplicationResponseEnvelope
	if err := json.Unmarshal(decryptedRespBytes1, &appResp1); err != nil {
		t.Fatalf("Failed to unmarshal response envelope: %v", err)
	}

	if appResp1.StatusCode != http.StatusOK || !strings.Contains(appResp1.Body, `"status":"ok"`) {
		t.Errorf("Unexpected app response 1: code=%d body=%s", appResp1.StatusCode, appResp1.Body)
	}

	// 8. Confirm Pairing for Device 2 (Simultaneous Multi-Device Isolation)
	ch2, _ := pairingEngine.CreateChallenge(5 * time.Minute)
	dev2ID := "dev-phone-02"
	dev2ProofSig, _ := pairingEngine.GenerateProofForChallenge(ch2.ID, dev2ID)
	confirmBody2 := map[string]string{
		"challenge_id":      ch2.ID,
		"device_id":         dev2ID,
		"device_name":       "Pixel 9 Pro",
		"device_public_key": "pubkey-android-02",
		"proof_signature":   dev2ProofSig,
	}

	confirmBytes2, _ := json.Marshal(confirmBody2)
	confirmResp2, err := http.Post(chResp.ConfirmURL, "application/json", bytes.NewReader(confirmBytes2))
	if err != nil || confirmResp2.StatusCode != http.StatusOK {
		t.Fatalf("Pairing confirmation failed for device 2: %v", err)
	}
	defer confirmResp2.Body.Close()

	var paired2 struct {
		Status        string `json:"status"`
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
		MachineID     string `json:"machine_id"`
		DeviceID      string `json:"device_id"`
		BrokerURL     string `json:"broker_url"`
	}
	_ = json.NewDecoder(confirmResp2.Body).Decode(&paired2)

	client2WSSURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", brokerWSURL, paired2.MachineID, paired2.DeviceID, paired2.SessionID)
	header2 := http.Header{}
	header2.Set("Authorization", "Bearer "+paired2.SessionToken)

	client2Conn, _, err := dialer.Dial(client2WSSURL, header2)
	if err != nil {
		t.Fatalf("Failed client 2 WSS connection: %v", err)
	}
	defer client2Conn.Close()

	// 9. Revoke Device 1 -> Verify Device 1 Terminated, Device 2 Unaffected
	revokeURL := fmt.Sprintf("%s/api/v1/internal/devices/revoke", brokerHTTPServer.URL)
	revokePayload := map[string]string{"machine_id": cfg.HostID, "device_id": dev1ID}
	revokeBytes, _ := json.Marshal(revokePayload)

	revokeReq, _ := http.NewRequest("POST", revokeURL, bytes.NewReader(revokeBytes))
	revokeReq.Header.Set("Authorization", "Bearer "+cfg.BrokerSecret)
	revokeReq.Header.Set("Content-Type", "application/json")

	revokeResp, err := http.DefaultClient.Do(revokeReq)
	if err != nil || revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to revoke device 1: %v", err)
	}
	defer revokeResp.Body.Close()

	time.Sleep(100 * time.Millisecond)

	// Device 1 write frame -> MUST fail
	err = client1Conn.WriteJSON(&frame1)
	if err == nil {
		_ = client1Conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		var dummy transport.FrameEnvelope
		err = client1Conn.ReadJSON(&dummy)
		if err == nil {
			t.Errorf("Expected device 1 connection to be closed after revocation")
		}
	}

	// Device 2 frame -> MUST still succeed
	client2E2E, _ := security.NewE2ESession(paired2.SessionID, paired2.DeviceID, paired2.SessionSecret, 24*time.Hour)
	appReq2 := transport.ApplicationRequestEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       paired2.SessionID,
		RequestID:       "req_e2e_002",
		Method:          "GET",
		Path:            "/api/v1/health",
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}
	appReqBytes2, _ := json.Marshal(appReq2)
	cipherReq2, _ := client2E2E.Encrypt(security.DirClientToHost, appReqBytes2, 1, time.Now().UnixNano())

	frame2 := transport.FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       paired2.SessionID,
		Type:            "data",
		EncryptedData:   cipherReq2,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := client2Conn.WriteJSON(&frame2); err != nil {
		t.Fatalf("Device 2 frame write failed: %v", err)
	}
}
