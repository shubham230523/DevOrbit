package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
		ChallengeID    string `json:"challenge_id"`
		PairingPayload string `json:"pairing_payload"`
	}
	_ = json.NewDecoder(resCh.Body).Decode(&ch)

	if ch.ChallengeID == "" || ch.PairingPayload == "" {
		t.Errorf("Invalid challenge response payload")
	}

	// 3. Test Confirm Pairing Endpoint
	proofSig := pairingEngine.GenerateProof(ch.PairingPayload, "phone-test-01")
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
}
