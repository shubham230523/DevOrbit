package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestTransport_HTTPAPIEndpointsAndPairing(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_transport_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	policy := security.NewDefaultCommandPolicy()
	pairingEngine := security.NewPairingEngine("host-test-01", "Test Host", "secret-32-bytes-key")

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storage)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	server := NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	server.SetPairingEngine(pairingEngine)

	provisionedSession := ""
	provisionedDevice := ""
	provisionedSecret := ""
	server.SetSessionProvisioner(func(sessionID, deviceID, sessionSecret string) error {
		provisionedSession = sessionID
		provisionedDevice = deviceID
		provisionedSecret = sessionSecret
		return nil
	})

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Test Health Endpoint
	res, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("Health check failed: %v", err)
	}

	// 2. Test Pairing Challenge Endpoint
	resCh, err := http.Post(ts.URL+"/api/v1/pairing/challenge", "application/json", nil)
	if err != nil || resCh.StatusCode != http.StatusOK {
		t.Fatalf("Pairing challenge API failed: %v", err)
	}

	var ch struct {
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
	_ = json.NewDecoder(resCh.Body).Decode(&ch)

	if ch.ChallengeID == "" || ch.PairingPayload == "" || ch.PairingSecret == "" {
		t.Errorf("Invalid challenge response payload: %+v", ch)
	}

	// Verify confirm_url is absolute URL
	if !strings.HasPrefix(ch.ConfirmURL, "http://") && !strings.HasPrefix(ch.ConfirmURL, "https://") {
		t.Errorf("Expected confirm_url to be absolute URL, got: %s", ch.ConfirmURL)
	}

	// 3. Test Confirm Pairing Endpoint
	proofSig, err := pairingEngine.GenerateProofForChallenge(ch.ChallengeID, "phone-test-01")
	if err != nil {
		t.Fatalf("Failed to generate proof: %v", err)
	}
	confirmReq := map[string]string{
		"challenge_id":      ch.ChallengeID,
		"device_id":         "phone-test-01",
		"device_name":       "Test Device",
		"device_public_key": "pubkey-12345",
		"proof_signature":   proofSig,
	}
	confirmBody, _ := json.Marshal(confirmReq)

	resConf, err := http.Post(ts.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(confirmBody))
	if err != nil || resConf.StatusCode != http.StatusOK {
		t.Fatalf("Pairing confirm API failed: %v", err)
	}

	var confResp struct {
		Status        string `json:"status"`
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
		MachineID     string `json:"machine_id"`
		DeviceID      string `json:"device_id"`
	}
	_ = json.NewDecoder(resConf.Body).Decode(&confResp)

	if confResp.Status != "success" || confResp.SessionToken == "" || confResp.SessionID == "" || confResp.SessionSecret == "" {
		t.Fatalf("Incomplete pairing confirm response: %+v", confResp)
	}

	// 4. Verify Session Provisioning was triggered on Server
	if provisionedSession != confResp.SessionID || provisionedDevice != "phone-test-01" || provisionedSecret != confResp.SessionSecret {
		t.Fatalf("Session provisioner mismatch! Expected (%s, phone-test-01, %s), got (%s, %s, %s)",
			confResp.SessionID, confResp.SessionSecret, provisionedSession, provisionedDevice, provisionedSecret)
	}

	// 5. Test Replaying Consumed Challenge Fails
	resReplay, err := http.Post(ts.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(confirmBody))
	if err != nil || resReplay.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected replayed challenge to be rejected with 400 Bad Request, got: %v", resReplay)
	}
}
