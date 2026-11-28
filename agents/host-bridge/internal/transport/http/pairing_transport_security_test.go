package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type failingDeleteSecretStore struct{}

func (f *failingDeleteSecretStore) Name() string    { return "failing-delete-store" }
func (f *failingDeleteSecretStore) Available() bool { return true }
func (f *failingDeleteSecretStore) Store(scope, id string, secret []byte) (string, error) {
	return "ref:" + scope + ":" + id, nil
}
func (f *failingDeleteSecretStore) Load(scope, id, ref string) ([]byte, error) {
	return nil, security.ErrSecretNotFound
}
func (f *failingDeleteSecretStore) Delete(scope, id, ref string) error {
	return os.ErrPermission
}

func TestPairingTransport_MultiValuedForwardedProtoAndRevokePersistenceFailure(t *testing.T) {
	srv, _, mux, tlsSrv, _, hostSecret := newProdPairingSecEnv(t)

	// 1. Multi-valued X-Forwarded-Proto from trusted proxy must be rejected (403)
	recMulti := httptest.NewRecorder()
	reqMulti := httptest.NewRequest(http.MethodPost, "/api/v1/pairing/challenge", strings.NewReader(`{}`))
	reqMulti.RemoteAddr = "10.10.0.50:443" // Trusted proxy IP
	reqMulti.Header.Add("X-Forwarded-Proto", "http")
	reqMulti.Header.Add("X-Forwarded-Proto", "https")
	reqMulti.Header.Set("Authorization", "Bearer "+hostSecret)
	mux.ServeHTTP(recMulti, reqMulti)
	if recMulti.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for ambiguous multi-valued X-Forwarded-Proto header, got %d", recMulti.Code)
	}

	// 2. If SessionStore persistence fails during /api/v1/sessions/revoke, HTTP handler must NOT return 200 OK,
	//    while still revoking the session in memory immediately.
	store := srv.SessionStore()
	store.SetSecretStore(&failingDeleteSecretStore{})
	if err := store.EnablePersistence(t.TempDir() + "/sessions.json"); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}
	if _, err := store.RegisterSession("sess-rev-err", "dev-rev-err", "tok-rev-err-32bytes!", "sec-rev-err-32bytes-master-01!!", time.Now().UTC().Add(time.Hour), security.NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}

	reqRev, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/sessions/revoke", strings.NewReader(`{"session_id":"sess-rev-err"}`))
	reqRev.Header.Set("Authorization", "Bearer "+hostSecret)
	respRev, err := tlsSrv.Client().Do(reqRev)
	if err != nil {
		t.Fatalf("Do revoke failed: %v", err)
	}
	respRev.Body.Close()
	if respRev.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500 when revocation durability persistence fails, got %d", respRev.StatusCode)
	}
	if _, activeErr := store.GetActiveSession("sess-rev-err", "dev-rev-err"); activeErr != security.ErrSessionRevoked {
		t.Fatalf("expected session to still be revoked immediately in memory despite persistence failure, got: %v", activeErr)
	}
}

type mockSecStoreHTTP struct {
	deleteErr error
	secrets   map[string]string
}

func (m *mockSecStoreHTTP) Name() string    { return "mock-http-sec-store" }
func (m *mockSecStoreHTTP) Available() bool { return true }
func (m *mockSecStoreHTTP) Store(scope, id string, secret []byte) (string, error) {
	m.secrets[scope+":"+id] = string(secret)
	return "ref:" + scope + ":" + id, nil
}
func (m *mockSecStoreHTTP) Load(scope, id, ref string) ([]byte, error) {
	v, ok := m.secrets[scope+":"+id]
	if !ok {
		return nil, security.ErrSecretNotFound
	}
	return []byte(v), nil
}
func (m *mockSecStoreHTTP) Delete(scope, id, ref string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.secrets, scope+":"+id)
	return nil
}

type mockBrokerAuthorizerHTTP struct {
	authErr          error
	revokeDevErr     error
	closeSessErr     error
	revokeDevCalled  []string
	closeSessCalled  []string
	lastAuthSessID   string
	lastAuthToken    string
}

func (m *mockBrokerAuthorizerHTTP) AuthorizeSession(ctx context.Context, machineID, deviceID, sessionID, clientToken, hostToken string, expiresAt time.Time) error {
	m.lastAuthSessID = sessionID
	m.lastAuthToken = clientToken
	return m.authErr
}

func (m *mockBrokerAuthorizerHTTP) RevokeDevice(ctx context.Context, machineID, deviceID string) error {
	m.revokeDevCalled = append(m.revokeDevCalled, deviceID)
	return m.revokeDevErr
}

func (m *mockBrokerAuthorizerHTTP) CloseSession(ctx context.Context, machineID, sessionID string) error {
	m.closeSessCalled = append(m.closeSessCalled, sessionID)
	return m.closeSessErr
}

func TestPairingRollbackAndBrokerRevocationErrorPropagation(t *testing.T) {
	srv, engine, _, tlsSrv, _, hostSecret := newProdPairingSecEnv(t)
	persistPath := filepath.Join(t.TempDir(), "sessions.json")
	ss := &mockSecStoreHTTP{secrets: make(map[string]string)}
	srv.SessionStore().SetSecretStore(ss)
	if err := srv.SessionStore().EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}

	makeConfirmBody := func(devID, nonce string) []byte {
		ch, _ := engine.CreateChallenge(5 * time.Minute)
		ts := strconv.FormatInt(time.Now().UTC().UnixMicro(), 10)
		proof, _ := engine.GenerateProofForChallenge(ch.ID, devID, ts, nonce)
		b, _ := json.Marshal(map[string]string{
			"challenge_id":    ch.ID,
			"device_id":       devID,
			"device_name":     "Phone",
			"timestamp":       ts,
			"nonce":           nonce,
			"proof_signature": proof,
		})
		return b
	}

	// 1. Broker authorization fails AFTER local RegisterSession -> rollback writes durable tombstone
	authMock := &mockBrokerAuthorizerHTTP{authErr: errors.New("broker authorize unreachable")}
	srv.SetBrokerAuthorizer(authMock)
	respAuthFail, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(makeConfirmBody("dev-rb-auth", "nonce-rb-auth-0001")))
	if err != nil {
		t.Fatalf("Post confirm failed: %v", err)
	}
	authFailBody, _ := io.ReadAll(respAuthFail.Body)
	respAuthFail.Body.Close()
	if respAuthFail.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 on broker auth failure, got %d (%s)", respAuthFail.StatusCode, string(authFailBody))
	}
	if authMock.lastAuthSessID == "" {
		t.Fatalf("expected AuthorizeSession to be called with generated session ID")
	}
	// Restart store against persistPath -> rolled-back session MUST be rejected as revoked
	restarted1 := security.NewSessionStore()
	restarted1.SetSecretStore(ss)
	_ = restarted1.EnablePersistence(persistPath)
	_ = restarted1.LoadPersistedSessions()
	if _, authErr := restarted1.AuthenticateBearer(authMock.lastAuthToken, authMock.lastAuthSessID, "dev-rb-auth"); authErr != security.ErrSessionRevoked {
		t.Fatalf("expected rolled-back session to remain revoked after restart, got: %v", authErr)
	}

	// 2. SessionProvisioner fails AFTER AuthorizeSession -> revokes BrokerClient session, remote device, & rolls back local store
	authMock.authErr = nil
	var revokedViaCallback []string
	srv.SetSessionRevoker(func(sessionID, deviceID string) error {
		revokedViaCallback = append(revokedViaCallback, sessionID+":"+deviceID)
		return nil
	})
	srv.SetSessionProvisioner(func(sessionID, deviceID, masterSecret string, expiresAt time.Time) error {
		return errors.New("local broker client provisioning failed")
	})
	respProvFail, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(makeConfirmBody("dev-rb-prov", "nonce-rb-prov-0001")))
	if err != nil {
		t.Fatalf("Post confirm failed: %v", err)
	}
	provFailBody, _ := io.ReadAll(respProvFail.Body)
	respProvFail.Body.Close()
	if respProvFail.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 on sessionProvisioner failure, got %d (%s)", respProvFail.StatusCode, string(provFailBody))
	}
	if len(revokedViaCallback) != 1 || !strings.HasPrefix(revokedViaCallback[0], authMock.lastAuthSessID+":") {
		t.Fatalf("expected sessionRevoker callback for %s, got %v", authMock.lastAuthSessID, revokedViaCallback)
	}
	if len(authMock.revokeDevCalled) != 1 || authMock.revokeDevCalled[0] != "dev-rb-prov" {
		t.Fatalf("expected remote RevokeDevice rollback call for dev-rb-prov, got %v", authMock.revokeDevCalled)
	}

	// 3. Rollback error during provisioning failure preserves original error + rollback error without leaking secret
	ss.deleteErr = errors.New("secret delete permission failure")
	srv.SetSessionProvisioner(func(sessionID, deviceID, masterSecret string, expiresAt time.Time) error {
		return errors.New("provision boom")
	})
	respRBErr, err := tlsSrv.Client().Post(tlsSrv.URL+"/api/v1/pairing/confirm", "application/json", bytes.NewReader(makeConfirmBody("dev-rb-delfail", "nonce-rb-df-0001")))
	if err != nil {
		t.Fatalf("Post confirm failed: %v", err)
	}
	rbErrBody, _ := io.ReadAll(respRBErr.Body)
	respRBErr.Body.Close()
	if respRBErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", respRBErr.StatusCode)
	}
	if !strings.Contains(string(rbErrBody), "provision boom") || !strings.Contains(string(rbErrBody), "rollback errors") {
		t.Fatalf("expected original + rollback error in response, got: %s", string(rbErrBody))
	}
	if strings.Contains(string(rbErrBody), authMock.lastAuthToken) {
		t.Fatalf("error response leaked session token!")
	}
	ss.deleteErr = nil

	// 4. BrokerClient / BrokerAuthorizer revocation error propagation on /api/v1/sessions/revoke
	_, _ = srv.SessionStore().RegisterSession("sess-prop-1", "dev-prop-1", "tok-prop-1-32bytes-secret!", "sec-prop-1-32bytes-master-01!", time.Now().UTC().Add(time.Hour), security.NewFullSessionPermissions())
	_, _ = srv.SessionStore().RegisterSession("sess-prop-2", "dev-prop-2", "tok-prop-2-32bytes-secret!", "sec-prop-2-32bytes-master-01!", time.Now().UTC().Add(time.Hour), security.NewFullSessionPermissions())

	// 4a. sessionRevoker error -> 500, session STILL revoked in memory
	srv.SetSessionRevoker(func(sessionID, deviceID string) error {
		return errors.New("broker client disk failure")
	})
	reqRevA, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/sessions/revoke", strings.NewReader(`{"session_id":"sess-prop-1"}`))
	reqRevA.Header.Set("Authorization", "Bearer "+hostSecret)
	respRevA, err := tlsSrv.Client().Do(reqRevA)
	if err != nil {
		t.Fatalf("Do revoke failed: %v", err)
	}
	respRevA.Body.Close()
	if respRevA.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when sessionRevoker fails, got %d", respRevA.StatusCode)
	}
	if _, activeErr := srv.SessionStore().GetActiveSession("sess-prop-1", "dev-prop-1"); activeErr != security.ErrSessionRevoked {
		t.Fatalf("expected sess-prop-1 to still be revoked in memory, got: %v", activeErr)
	}

	// 4b. Remote CloseSession error on session-only revoke -> 502 (and CloseSession invoked, NOT RevokeDevice)
	srv.SetSessionRevoker(func(sessionID, deviceID string) error { return nil })
	authMock.revokeDevCalled = nil
	authMock.closeSessCalled = nil
	authMock.closeSessErr = errors.New("broker remote close timeout")
	reqRevB, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/sessions/revoke", strings.NewReader(`{"session_id":"sess-prop-2"}`))
	reqRevB.Header.Set("Authorization", "Bearer "+hostSecret)
	respRevB, err := tlsSrv.Client().Do(reqRevB)
	if err != nil {
		t.Fatalf("Do revoke failed: %v", err)
	}
	respRevB.Body.Close()
	if respRevB.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 when remote CloseSession fails, got %d", respRevB.StatusCode)
	}
	if len(authMock.closeSessCalled) != 1 || authMock.closeSessCalled[0] != "sess-prop-2" || len(authMock.revokeDevCalled) != 0 {
		t.Fatalf("expected CloseSession(sess-prop-2) only, got close=%v revDev=%v", authMock.closeSessCalled, authMock.revokeDevCalled)
	}

	// 4c. Remote RevokeDevice error on device-wide revoke -> 502
	authMock.closeSessErr = nil
	authMock.revokeDevErr = errors.New("broker remote device revoke error")
	reqRevC, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/sessions/revoke", strings.NewReader(`{"device_id":"dev-prop-2"}`))
	reqRevC.Header.Set("Authorization", "Bearer "+hostSecret)
	respRevC, err := tlsSrv.Client().Do(reqRevC)
	if err != nil {
		t.Fatalf("Do revoke failed: %v", err)
	}
	respRevC.Body.Close()
	if respRevC.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 when remote RevokeDevice fails, got %d", respRevC.StatusCode)
	}

	// 4d. Absent-from-memory session revoke via HTTP writes durable tombstone and returns 200
	authMock.revokeDevErr = nil
	reqRevAbs, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/sessions/revoke", strings.NewReader(`{"session_id":"sess-never-loaded"}`))
	reqRevAbs.Header.Set("Authorization", "Bearer "+hostSecret)
	respRevAbs, err := tlsSrv.Client().Do(reqRevAbs)
	if err != nil {
		t.Fatalf("Do revoke absent failed: %v", err)
	}
	respRevAbs.Body.Close()
	if respRevAbs.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when absent session tombstone persists cleanly, got %d", respRevAbs.StatusCode)
	}
	restarted2 := security.NewSessionStore()
	restarted2.SetSecretStore(ss)
	_ = restarted2.EnablePersistence(persistPath)
	_ = restarted2.LoadPersistedSessions()
	if _, err := restarted2.GetActiveSession("sess-never-loaded", "any-dev"); err != security.ErrSessionRevoked {
		t.Fatalf("expected ErrSessionRevoked after restart for HTTP-revoked absent session, got: %v", err)
	}
}
