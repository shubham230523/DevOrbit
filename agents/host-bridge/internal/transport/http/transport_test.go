package http

import (
	"bytes"
	"encoding/json"
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
	server.SetSessionProvisioner(func(sessionID, deviceID, sessionSecret string, expiry time.Time) error {
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

	// 5. Test Replaying Consumed Challenge by SAME device returns 200 (cached response).
	// This is the idempotent retry behaviour: if the mobile lost the response due to
	// a network timeout, it can safely retry the same confirm request and get back
	// the same session credentials.
	resReplay, err := http.Post(ts.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewBuffer(confirmBody))
	if err != nil || resReplay.StatusCode != http.StatusOK {
		t.Fatalf("Expected same-device replay to return 200 OK (cached response), got: %v", resReplay)
	}
}

func TestTransport_AdvertisedURLTrustAndProxyValidation(t *testing.T) {
	tempRoot, _ := os.MkdirTemp("", "devorbit_url_test_*")
	defer os.RemoveAll(tempRoot)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	pairingEngine := security.NewPairingEngine("host-test-02", "Test Host 2", "secret-32-bytes-key")
	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(filesystem.NewStorageEngine(jail, 10*1024*1024))
	psSvc := application.NewProcessService(process.NewProcessRunner(security.NewDefaultCommandPolicy(), jail))
	gsSvc := application.NewGitService(git.NewGitClient(jail))
	dsSvc := application.NewDeviceService(adb.NewADBClient("adb"))

	server := NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	server.SetPairingEngine(pairingEngine)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. Missing Production Advertised URL -> Fails with HTTP 500
	server.SetIsProduction(true)
	server.SetAdvertisedURL("")
	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/pairing/challenge", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected HTTP 500 for missing production advertised URL, got %d", rec.Code)
	}

	// 2. Invalid Production Advertised URL (insecure http) -> Fails with HTTP 500
	server.SetAdvertisedURL("http://insecure.example.com")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected HTTP 500 for insecure http URL in production, got %d", rec.Code)
	}

	// 3. Valid Production HTTPS Advertised URL -> Succeeds
	server.SetAdvertisedURL("https://prod.devorbit.net")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected HTTP 200 for valid HTTPS URL in production, got %d", rec.Code)
	}
	var resp3 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp3)
	if resp3["confirm_url"] != "https://prod.devorbit.net/api/v1/pairing/confirm" {
		t.Errorf("Expected confirm_url to match prod advertised URL, got %v", resp3["confirm_url"])
	}

	// 4. Development mode: Valid LAN HTTP Advertised URL -> Succeeds
	server.SetIsProduction(false)
	server.SetAdvertisedURL("http://192.168.1.100:8082")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected HTTP 200 for valid LAN HTTP URL, got %d", rec.Code)
	}
	var resp4 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp4)
	if resp4["confirm_url"] != "http://192.168.1.100:8082/api/v1/pairing/confirm" {
		t.Errorf("Expected confirm_url to match LAN advertised URL, got %v", resp4["confirm_url"])
	}

	// 5. Untrusted Forwarded Headers -> Ignored when not from trusted proxy
	server.SetAdvertisedURL("")
	server.SetTrustedProxies([]string{"10.0.0.1"})
	reqUntrusted, _ := http.NewRequest(http.MethodPost, "/api/v1/pairing/challenge", nil)
	reqUntrusted.RemoteAddr = "192.168.1.50:54321" // Not trusted proxy
	reqUntrusted.Header.Set("X-Forwarded-Proto", "https")
	reqUntrusted.Header.Set("X-Forwarded-Host", "attacker-injected.com")
	reqUntrusted.Host = "192.168.1.100:8082"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, reqUntrusted)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected HTTP 200, got %d", rec.Code)
	}
	var resp5 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp5)
	if strings.Contains(resp5["confirm_url"].(string), "attacker-injected.com") {
		t.Errorf("Vulnerability: untrusted X-Forwarded-Host was accepted! Got: %v", resp5["confirm_url"])
	}

	// 6. Trusted Reverse Proxy -> Uses forwarded proto and host
	reqTrusted, _ := http.NewRequest(http.MethodPost, "/api/v1/pairing/challenge", nil)
	reqTrusted.RemoteAddr = "10.0.0.1:443" // Trusted proxy
	reqTrusted.Header.Set("X-Forwarded-Proto", "https")
	reqTrusted.Header.Set("X-Forwarded-Host", "proxy.trusted.com")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, reqTrusted)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected HTTP 200, got %d", rec.Code)
	}
	var resp6 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp6)
	if resp6["confirm_url"] != "https://proxy.trusted.com/api/v1/pairing/confirm" {
		t.Errorf("Expected confirm_url to use trusted forwarded headers, got %v", resp6["confirm_url"])
	}

	// 7. Host Header Injection with invalid characters -> Sanitized to safe default
	reqHostInjection, _ := http.NewRequest(http.MethodPost, "/api/v1/pairing/challenge", nil)
	reqHostInjection.RemoteAddr = "192.168.1.50:54321"
	reqHostInjection.Host = "evil.com/path@victim"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, reqHostInjection)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected HTTP 200, got %d", rec.Code)
	}
	var resp7 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp7)
	if strings.Contains(resp7["confirm_url"].(string), "evil.com") {
		t.Errorf("Host header injection vulnerability! Got: %v", resp7["confirm_url"])
	}
}
