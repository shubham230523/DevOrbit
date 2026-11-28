package http_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
)

func newProdPairingSecEnv(t *testing.T) (*httptransport.Server, *security.PairingEngine, *http.ServeMux, *httptest.Server, *httptest.Server, string) {
	t.Helper()
	wsDir := t.TempDir()
	jail := security.NewWorkspaceJail([]string{wsDir})
	policy := security.NewDefaultCommandPolicy()
	hostSecret := "prod-host-admin-secret-32bytes-01!"
	pairingEngine := security.NewPairingEngine("host-prod-01", "Prod Host", hostSecret)

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	srv := httptransport.NewServer(
		application.NewWorkspaceService(jail),
		application.NewFilesystemService(storage),
		application.NewProcessService(procRunner),
		application.NewGitService(gitClient),
		application.NewDeviceService(adbClient),
	)
	srv.SetPairingEngine(pairingEngine)
	srv.SetHostToken(hostSecret)
	srv.SetIsProduction(true)
	srv.SetAdvertisedURL("https://bridge.prod.example.com")
	srv.SetTrustedProxies([]string{"10.10.0.50"})
	srv.SetBrokerEndpoints("https://broker.prod.example.com", "wss://broker.prod.example.com", hostSecret)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	tlsSrv := httptest.NewTLSServer(mux)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(func() {
		tlsSrv.Close()
		httpSrv.Close()
		_ = os.RemoveAll(wsDir)
	})

	return srv, pairingEngine, mux, tlsSrv, httpSrv, hostSecret
}

func TestPairingTransport_HostAdminRequiresConfidentialTransportInProduction(t *testing.T) {
	_, _, mux, tlsSrv, httpSrv, hostSecret := newProdPairingSecEnv(t)

	for _, endpoint := range []struct {
		path string
		body string
	}{
		{"/api/v1/pairing/challenge", `{}`},
		{"/api/v1/sessions/revoke", `{"session_id":"sess-nonexistent"}`},
	} {
		t.Run("plaintext_"+endpoint.path, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+endpoint.path, strings.NewReader(endpoint.body))
			req.Header.Set("Authorization", "Bearer "+hostSecret)
			resp, err := httpSrv.Client().Do(req)
			if err != nil {
				t.Fatalf("Do failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("expected 403 for plaintext host-admin in production, got %d", resp.StatusCode)
			}
		})

		t.Run("untrusted_x_forwarded_proto_"+endpoint.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body))
			req.RemoteAddr = "192.168.1.99:50000" // NOT a trusted proxy
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("Authorization", "Bearer "+hostSecret)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 when untrusted IP spoofs X-Forwarded-Proto: https, got %d", rec.Code)
			}
		})

		t.Run("loopback_without_trusted_proxy_headers_"+endpoint.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body))
			req.RemoteAddr = "127.0.0.1:50000"
			req.Header.Set("Authorization", "Bearer "+hostSecret)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for direct loopback plaintext in production without trusted proxy HTTPS header, got %d", rec.Code)
			}
		})

		t.Run("trusted_proxy_https_"+endpoint.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body))
			req.RemoteAddr = "10.10.0.50:443" // Configured trusted proxy
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("Authorization", "Bearer "+hostSecret)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 via trusted HTTPS reverse proxy, got %d (%s)", rec.Code, rec.Body.String())
			}
		})

		t.Run("native_tls_"+endpoint.path, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+endpoint.path, strings.NewReader(endpoint.body))
			req.Header.Set("Authorization", "Bearer "+hostSecret)
			resp, err := tlsSrv.Client().Do(req)
			if err != nil {
				t.Fatalf("TLS Do failed: %v", err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 via TLS, got %d (%s)", resp.StatusCode, string(raw))
			}
		})

		t.Run("native_tls_wrong_token_"+endpoint.path, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+endpoint.path, strings.NewReader(endpoint.body))
			req.Header.Set("Authorization", "Bearer wrong-host-secret")
			resp, err := tlsSrv.Client().Do(req)
			if err != nil {
				t.Fatalf("TLS Do failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected 401 for wrong host-admin bearer on TLS, got %d", resp.StatusCode)
			}
		})
	}
}

func TestPairingTransport_ConfirmRequiresConfidentialTransportAndEnforcesAntiReplay(t *testing.T) {
	srv, engine, mux, tlsSrv, httpSrv, hostSecret := newProdPairingSecEnv(t)

	// 1. Create pairing challenge over TLS
	reqCh, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/pairing/challenge", nil)
	reqCh.Header.Set("Authorization", "Bearer "+hostSecret)
	respCh, err := tlsSrv.Client().Do(reqCh)
	if err != nil || respCh.StatusCode != http.StatusOK {
		t.Fatalf("challenge over TLS failed: %v", err)
	}
	var ch struct {
		ChallengeID    string `json:"challenge_id"`
		HostID         string `json:"host_id"`
		PairingPayload string `json:"pairing_payload"`
		PairingSecret  string `json:"pairing_secret"`
		ConfirmURL     string `json:"confirm_url"`
		ExpiresAt      string `json:"expires_at"`
	}
	_ = json.NewDecoder(respCh.Body).Decode(&ch)
	respCh.Body.Close()

	if ch.ConfirmURL != "https://bridge.prod.example.com/api/v1/pairing/confirm" {
		t.Fatalf("unexpected production confirm_url: %q", ch.ConfirmURL)
	}

	devID := "dev-prod-01"
	ts1 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonce1 := "nonce-prod-confirm-0001"
	proof1, err := engine.GenerateProofForChallenge(ch.ChallengeID, devID, ts1, nonce1)
	if err != nil {
		t.Fatalf("GenerateProofForChallenge failed: %v", err)
	}
	confirmPayload, _ := json.Marshal(map[string]string{
		"challenge_id":    ch.ChallengeID,
		"device_id":       devID,
		"device_name":     "Pixel Prod",
		"timestamp":       ts1,
		"nonce":           nonce1,
		"proof_signature": proof1,
	})

	// 2. Plaintext HTTP pairing/confirm MUST be rejected in production (403) even with valid proof
	plainResp, err := httpSrv.Client().Post(httpSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(confirmPayload))
	if err != nil {
		t.Fatalf("plaintext post failed: %v", err)
	}
	plainBody, _ := io.ReadAll(plainResp.Body)
	plainResp.Body.Close()
	if plainResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for plaintext pairing confirm in production, got %d", plainResp.StatusCode)
	}
	if strings.Contains(string(plainBody), ch.PairingSecret) {
		t.Fatalf("error response leaked pairing secret")
	}

	// 3. Untrusted X-Forwarded-Proto: https MUST NOT bypass confirm TLS check in production
	recSpoof := httptest.NewRecorder()
	reqSpoof := httptest.NewRequest(http.MethodPost, "/api/v1/pairing/confirm", bytes.NewReader(confirmPayload))
	reqSpoof.RemoteAddr = "192.168.1.25:12345"
	reqSpoof.Header.Set("X-Forwarded-Proto", "https")
	mux.ServeHTTP(recSpoof, reqSpoof)
	if recSpoof.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for spoofed X-Forwarded-Proto on confirm, got %d", recSpoof.Code)
	}

	// 4. Confirm over TLS succeeds and issues session credentials
	tlsResp, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(confirmPayload))
	if err != nil {
		t.Fatalf("TLS confirm failed: %v", err)
	}
	var paired struct {
		Status        string `json:"status"`
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
	}
	_ = json.NewDecoder(tlsResp.Body).Decode(&paired)
	tlsResp.Body.Close()
	if tlsResp.StatusCode != http.StatusOK || paired.SessionToken == "" || paired.SessionID == "" || paired.SessionSecret == "" {
		t.Fatalf("expected 200 and full session credentials over TLS, got %d %+v", tlsResp.StatusCode, paired)
	}

	// 5. Replaying exact captured confirm request fails with 403 (ErrReplayDetected)
	replayResp, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(confirmPayload))
	if err != nil {
		t.Fatalf("TLS replay check failed: %v", err)
	}
	replayBody, _ := io.ReadAll(replayResp.Body)
	replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for replayed confirm request, got %d", replayResp.StatusCode)
	}
	if strings.Contains(string(replayBody), paired.SessionToken) || strings.Contains(string(replayBody), paired.SessionSecret) {
		t.Fatalf("replay error leaked session credentials")
	}

	// 6. Legitimate same-device retry with fresh (timestamp, nonce, v1.1 proof) recovers same credentials BEFORE session activation
	tsRetry := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonceRetry := "nonce-prod-retry-0002"
	proofRetry, err := engine.GenerateProofForChallenge(ch.ChallengeID, devID, tsRetry, nonceRetry)
	if err != nil {
		t.Fatalf("retry proof generation failed: %v", err)
	}
	retryPayload, _ := json.Marshal(map[string]string{
		"challenge_id":    ch.ChallengeID,
		"device_id":       devID,
		"device_name":     "Pixel Prod",
		"timestamp":       tsRetry,
		"nonce":           nonceRetry,
		"proof_signature": proofRetry,
	})
	retryResp, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(retryPayload))
	if err != nil {
		t.Fatalf("TLS retry failed: %v", err)
	}
	var retried struct {
		SessionToken  string `json:"session_token"`
		SessionID     string `json:"session_id"`
		SessionSecret string `json:"session_secret"`
	}
	_ = json.NewDecoder(retryResp.Body).Decode(&retried)
	retryResp.Body.Close()
	if retryResp.StatusCode != http.StatusOK || retried.SessionToken != paired.SessionToken || retried.SessionSecret != paired.SessionSecret {
		t.Fatalf("expected idempotent retry to return original credentials, got %d %+v", retryResp.StatusCode, retried)
	}

	// 7. Activate session by calling an authenticated endpoint over E2E (without raw bearer)
	authKey, err := security.DeriveHTTPRequestAuthKey(paired.SessionSecret, paired.SessionID, devID)
	if err != nil {
		t.Fatalf("DeriveHTTPRequestAuthKey failed: %v", err)
	}
	httpE2E, err := security.NewChannelE2ESession(security.ChannelDirectHTTP, paired.SessionID, devID, paired.SessionSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewChannelE2ESession failed: %v", err)
	}
	creds := &secSessionCreds{
		sessionID: paired.SessionID,
		deviceID:  devID,
		token:     paired.SessionToken,
		secret:    paired.SessionSecret,
		authKey:   authKey,
		httpE2E:   httpE2E,
	}
	outer, inner, _ := doSecE2ERequest(t, httpSrv.URL, creds, 1, http.MethodGet, "/api/v1/workspaces", nil)
	if outer != http.StatusOK || inner != http.StatusOK {
		t.Fatalf("E2E request with newly paired session failed: outer=%d inner=%d", outer, inner)
	}

	// 8. Once session is activated/used, pairing retry MUST be permanently closed EVEN with a valid fresh v1.1 proof
	tsPostUse := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	noncePostUse := "nonce-post-activation-0003"
	proofPostUse := security.GenerateProofV11(ch.ChallengeID, ch.PairingPayload, ch.HostID, devID, ch.ExpiresAt, tsPostUse, noncePostUse, ch.PairingSecret)
	postUsePayload, _ := json.Marshal(map[string]string{
		"challenge_id":    ch.ChallengeID,
		"device_id":       devID,
		"device_name":     "Pixel Prod",
		"timestamp":       tsPostUse,
		"nonce":           noncePostUse,
		"proof_signature": proofPostUse,
	})
	postUseResp, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(postUsePayload))
	if err != nil {
		t.Fatalf("post-activation retry check failed: %v", err)
	}
	postUseBody, _ := io.ReadAll(postUseResp.Body)
	postUseResp.Body.Close()
	if postUseResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 after session activation closed retry window, got %d (%s)", postUseResp.StatusCode, string(postUseBody))
	}
	if strings.Contains(string(postUseBody), paired.SessionToken) || strings.Contains(string(postUseBody), paired.SessionSecret) {
		t.Fatalf("post-activation error leaked credentials")
	}

	// 8b. Revoking a freshly paired unused session over TLS ALSO closes its retry window
	chRev, _ := engine.CreateChallenge(5 * time.Minute)
	tsRev1 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonceRev1 := "nonce-rev-pair-0001"
	proofRev1, _ := engine.GenerateProofForChallenge(chRev.ID, "dev-rev-pair", tsRev1, nonceRev1)
	revPairBody, _ := json.Marshal(map[string]string{
		"challenge_id": chRev.ID, "device_id": "dev-rev-pair", "device_name": "Revoked Phone",
		"timestamp": tsRev1, "nonce": nonceRev1, "proof_signature": proofRev1,
	})
	revPairResp, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(revPairBody))
	if err != nil || revPairResp.StatusCode != http.StatusOK {
		t.Fatalf("confirm before revoke failed: %v", err)
	}
	var pairedRev struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(revPairResp.Body).Decode(&pairedRev)
	revPairResp.Body.Close()

	reqRevoke, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/sessions/revoke", strings.NewReader(`{"session_id":"`+pairedRev.SessionID+`"}`))
	reqRevoke.Header.Set("Authorization", "Bearer "+hostSecret)
	respRevoke, err := tlsSrv.Client().Do(reqRevoke)
	if err != nil || respRevoke.StatusCode != http.StatusOK {
		t.Fatalf("session revoke failed: %v", err)
	}
	respRevoke.Body.Close()

	tsRev2 := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
	nonceRev2 := "nonce-rev-pair-0002"
	proofRev2 := security.GenerateProofV11(chRev.ID, chRev.Payload, chRev.HostID, "dev-rev-pair", chRev.ExpiresAtStr, tsRev2, nonceRev2, chRev.Secret)
	revRetryBody, _ := json.Marshal(map[string]string{
		"challenge_id": chRev.ID, "device_id": "dev-rev-pair", "device_name": "Revoked Phone",
		"timestamp": tsRev2, "nonce": nonceRev2, "proof_signature": proofRev2,
	})
	revRetryResp, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(revRetryBody))
	if err != nil {
		t.Fatalf("post-revoke retry check failed: %v", err)
	}
	revRetryResp.Body.Close()
	if revRetryResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when retrying revoked pairing session, got %d", revRetryResp.StatusCode)
	}

	// 9. Verify BuildStartupPairingQRPayload in production emits HTTPS advertised URL and rejects unsafe setups
	chQR, _ := engine.CreateChallenge(5 * time.Minute)
	qrMap, err := srv.BuildStartupPairingQRPayload(chQR, "192.168.1.10", 8082, true)
	if err != nil {
		t.Fatalf("BuildStartupPairingQRPayload with native TLS failed: %v", err)
	}
	if qrMap["confirm_url"] != "https://bridge.prod.example.com/api/v1/pairing/confirm" {
		t.Fatalf("unexpected production QR confirm_url: %v", qrMap["confirm_url"])
	}

	// Without native TLS or trusted proxies, production QR must refuse to start
	srv.SetTrustedProxies(nil)
	if _, err := srv.BuildStartupPairingQRPayload(chQR, "192.168.1.10", 8082, false); err == nil {
		t.Fatalf("expected BuildStartupPairingQRPayload to fail in production without native TLS or trusted proxies")
	}
}
