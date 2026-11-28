package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockSecretStore struct {
	mu          sync.Mutex
	available   bool
	storeErr    error
	loadErr     error
	deleteErr   error
	secrets     map[string]string
	deleteCalls int
}

func newMockSecretStore(available bool) *mockSecretStore {
	return &mockSecretStore{
		available: available,
		secrets:   make(map[string]string),
	}
}

func (m *mockSecretStore) Name() string    { return "mock-secret-store" }
func (m *mockSecretStore) Available() bool { return m.available }

func (m *mockSecretStore) Store(scope, id string, secret []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.available {
		return "", ErrSecureStoreUnavailable
	}
	if m.storeErr != nil {
		return "", m.storeErr
	}
	key := scope + ":" + id
	m.secrets[key] = string(secret)
	return "mock-ref:" + key, nil
}

func (m *mockSecretStore) Load(scope, id, ref string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.available {
		return nil, ErrSecureStoreUnavailable
	}
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	val, ok := m.secrets[scope+":"+id]
	if !ok {
		return nil, ErrSecretNotFound
	}
	return []byte(val), nil
}

func (m *mockSecretStore) Delete(scope, id, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.secrets, scope+":"+id)
	return nil
}

func TestSessionStore_RegisterAndAuthenticate(t *testing.T) {
	store := NewSessionStore()
	expiry := time.Now().UTC().Add(1 * time.Hour)

	rec, err := store.RegisterSession("sess-01", "dev-01", "tok-secret-01", "master-secret-32-bytes-01234567", expiry, NewFullSessionPermissions())
	if err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}
	if rec.TokenHash == "" || rec.TokenHash == "tok-secret-01" {
		t.Fatalf("expected SHA-256 TokenHash instead of raw token")
	}
	if rec.DirectHTTPE2E == nil || len(rec.HTTPAuthKey) != 32 {
		t.Fatalf("expected DirectHTTPE2E and 32-byte HTTPAuthKey to be derived")
	}

	got, err := store.AuthenticateBearer("tok-secret-01", "sess-01", "dev-01")
	if err != nil {
		t.Fatalf("AuthenticateBearer failed: %v", err)
	}
	if got.SessionID != "sess-01" || got.DeviceID != "dev-01" {
		t.Fatalf("unexpected session identity: %+v", got)
	}

	if _, err := store.AuthenticateBearer("wrong-token", "sess-01", "dev-01"); err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound for wrong token, got %v", err)
	}
	if _, err := store.AuthenticateBearer("tok-secret-01", "sess-other", "dev-01"); err != ErrSessionBindingMismatch {
		t.Fatalf("expected ErrSessionBindingMismatch for wrong session_id, got %v", err)
	}
	if _, err := store.AuthenticateBearer("tok-secret-01", "sess-01", "dev-other"); err != ErrSessionBindingMismatch {
		t.Fatalf("expected ErrSessionBindingMismatch for wrong device_id, got %v", err)
	}
	if _, err := store.AuthenticateBearer("tok-secret-01", "", "dev-01"); err != ErrMissingBinding {
		t.Fatalf("expected ErrMissingBinding for empty session_id, got %v", err)
	}
	if _, err := store.AuthenticateBearer("tok-secret-01", "sess-01", ""); err != ErrMissingBinding {
		t.Fatalf("expected ErrMissingBinding for empty device_id, got %v", err)
	}
}

func TestSessionStore_ExpiryAndRevocation(t *testing.T) {
	store := NewSessionStore()
	if _, err := store.RegisterSession("sess-past", "dev-01", "tok-past", "master-secret-32-bytes-01234567", time.Now().UTC().Add(-1*time.Minute), nil); err != ErrSessionExpired {
		t.Fatalf("expected ErrSessionExpired for past expiry, got %v", err)
	}

	rec1, err := store.RegisterSession("sess-1", "dev-1", "tok-1", "master-secret-32-bytes-01234567", time.Now().UTC().Add(150*time.Millisecond), nil)
	if err != nil {
		t.Fatalf("RegisterSession sess-1 failed: %v", err)
	}
	_, err = store.RegisterSession("sess-2", "dev-1", "tok-2", "master-secret-32-bytes-01234567", time.Now().UTC().Add(1*time.Hour), nil)
	if err != nil {
		t.Fatalf("RegisterSession sess-2 failed: %v", err)
	}
	_, err = store.RegisterSession("sess-3", "dev-2", "tok-3", "master-secret-32-bytes-01234567", time.Now().UTC().Add(1*time.Hour), nil)
	if err != nil {
		t.Fatalf("RegisterSession sess-3 failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if _, err := store.AuthenticateBearer("tok-1", "sess-1", "dev-1"); err != ErrSessionExpired {
		t.Fatalf("expected ErrSessionExpired after TTL elapsed, got %v", err)
	}
	if rec1.IsAuthorized(PermissionFileRead) {
		t.Fatalf("expected expired session not to be authorized")
	}

	// Revoke device dev-1 -> sess-2 must be rejected, dev-2's sess-3 must stay active
	count, err := store.RevokeDevice("dev-1")
	if err != nil {
		t.Fatalf("RevokeDevice failed: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected revoked session count > 0")
	}
	if _, err := store.AuthenticateBearer("tok-2", "sess-2", "dev-1"); err != ErrSessionRevoked {
		t.Fatalf("expected ErrSessionRevoked for revoked device, got %v", err)
	}
	if _, err := store.GetActiveSession("sess-2", "dev-1"); err != ErrSessionRevoked {
		t.Fatalf("expected GetActiveSession to return ErrSessionRevoked, got %v", err)
	}
	if _, err := store.AuthenticateBearer("tok-3", "sess-3", "dev-2"); err != nil {
		t.Fatalf("expected unrelated device session sess-3 to remain active, got %v", err)
	}

	// Revoke sess-3 individually
	ok, err := store.RevokeSession("sess-3")
	if err != nil || !ok {
		t.Fatalf("expected RevokeSession(sess-3) to succeed, ok=%v err=%v", ok, err)
	}
	if _, err := store.AuthenticateBearer("tok-3", "sess-3", "dev-2"); err != ErrSessionRevoked {
		t.Fatalf("expected ErrSessionRevoked for sess-3, got %v", err)
	}
}

func TestSessionStore_NonceReplayAndClockSkew(t *testing.T) {
	store := NewSessionStore()
	rec, err := store.RegisterSession("sess-nonce", "dev-nonce", "tok-nonce", "master-secret-32-bytes-01234567", time.Now().UTC().Add(1*time.Hour), nil)
	if err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}

	now := time.Now().UTC()
	if err := rec.VerifyAndConsumeNonce("nonce-12345678", now, now); err != nil {
		t.Fatalf("expected fresh valid nonce to succeed, got %v", err)
	}
	if err := rec.VerifyAndConsumeNonce("nonce-12345678", now, now); err != ErrReplayDetected {
		t.Fatalf("expected replayed nonce to return ErrReplayDetected, got %v", err)
	}
	if err := rec.VerifyAndConsumeNonce("short", now, now); err != ErrReplayDetected {
		t.Fatalf("expected short nonce to return ErrReplayDetected, got %v", err)
	}
	if err := rec.VerifyAndConsumeNonce("nonce-old-12345", now.Add(-10*time.Minute), now); err != ErrInvalidTimestamp {
		t.Fatalf("expected stale timestamp to return ErrInvalidTimestamp, got %v", err)
	}
	if err := rec.VerifyAndConsumeNonce("nonce-fut-12345", now.Add(5*time.Minute), now); err != ErrInvalidTimestamp {
		t.Fatalf("expected future timestamp to return ErrInvalidTimestamp, got %v", err)
	}
}

func TestSessionStore_PersistencePermissionsAndNoPlaintextSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	persistPath := filepath.Join(tmpDir, "sessions.json")
	mockSS := newMockSecretStore(true)

	store1 := NewSessionStore()
	store1.SetSecretStore(mockSS)
	if err := store1.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}

	rawTokenRestricted := "tok-restricted-secret-value-0001"
	masterRestricted := "master-restricted-secret-value-0001!"
	rawTokenEmpty := "tok-empty-perms-secret-value-0002"
	masterEmpty := "master-empty-perms-secret-value-0002!"
	rawTokenNil := "tok-nil-perms-secret-value-0003"
	masterNil := "master-nil-perms-secret-value-0003!"

	// 1. Restricted permissions
	if _, err := store1.RegisterSession(
		"sess-restricted", "dev-1", rawTokenRestricted, masterRestricted,
		time.Now().UTC().Add(time.Hour),
		NewScopedSessionPermissions(PermissionWorkspaceList, PermissionFileRead),
	); err != nil {
		t.Fatalf("RegisterSession restricted failed: %v", err)
	}

	// 2. Explicitly empty permission map (0 permissions)
	if _, err := store1.RegisterSession(
		"sess-empty", "dev-2", rawTokenEmpty, masterEmpty,
		time.Now().UTC().Add(time.Hour),
		NewScopedSessionPermissions(),
	); err != nil {
		t.Fatalf("RegisterSession empty failed: %v", err)
	}

	// 3. Nil perms passed to RegisterSession intentionally assigns full permissions
	if _, err := store1.RegisterSession(
		"sess-nil-default", "dev-3", rawTokenNil, masterNil,
		time.Now().UTC().Add(time.Hour),
		nil,
	); err != nil {
		t.Fatalf("RegisterSession nil failed: %v", err)
	}

	rawBytes, err := os.ReadFile(persistPath)
	if err != nil {
		t.Fatalf("failed reading persisted session file: %v", err)
	}
	diskStr := string(rawBytes)
	for _, secret := range []string{rawTokenRestricted, masterRestricted, rawTokenEmpty, masterEmpty, rawTokenNil, masterNil} {
		if strings.Contains(diskStr, secret) {
			t.Fatalf("SECURITY FAILURE: persisted JSON contains raw token or plaintext master secret!")
		}
	}
	if strings.Contains(diskStr, `"session_secret"`) {
		t.Fatalf("SECURITY FAILURE: persisted JSON must never contain session_secret key")
	}

	// Reload into a fresh store and verify permissions did NOT escalate across restart
	store2 := NewSessionStore()
	store2.SetSecretStore(mockSS)
	if err := store2.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence store2 failed: %v", err)
	}
	if err := store2.LoadPersistedSessions(); err != nil {
		t.Fatalf("LoadPersistedSessions failed: %v", err)
	}

	recRestricted, err := store2.AuthenticateBearer(rawTokenRestricted, "sess-restricted", "dev-1")
	if err != nil {
		t.Fatalf("AuthenticateBearer restricted failed: %v", err)
	}
	if !recRestricted.IsAuthorized(PermissionFileRead) || recRestricted.IsAuthorized(PermissionFileWrite) {
		t.Fatalf("restricted permissions were not preserved across restart")
	}

	recEmpty, err := store2.AuthenticateBearer(rawTokenEmpty, "sess-empty", "dev-2")
	if err != nil {
		t.Fatalf("AuthenticateBearer empty failed: %v", err)
	}
	for _, p := range []ActionPermission{
		PermissionWorkspaceList, PermissionFileRead, PermissionFileWrite,
		PermissionProcessExecute, PermissionAIExecute, PermissionAgentApprove,
	} {
		if recEmpty.IsAuthorized(p) {
			t.Fatalf("PRIVILEGE ESCALATION: explicitly empty permission set became authorized for %s after restart", p)
		}
	}

	recNil, err := store2.AuthenticateBearer(rawTokenNil, "sess-nil-default", "dev-3")
	if err != nil {
		t.Fatalf("AuthenticateBearer nil-default failed: %v", err)
	}
	if !recNil.IsAuthorized(PermissionAgentApprove) || !recNil.IsAuthorized(PermissionFileWrite) {
		t.Fatalf("expected nil-default registration permissions to remain full across restart")
	}
}

func TestSessionStore_DefaultOSSecretStoreRoundTrip(t *testing.T) {
	defaultSS := NewDefaultSecretStore()
	if !defaultSS.Available() {
		t.Skipf("default OS secret store (%s) not available in this headless environment", defaultSS.Name())
	}

	tmpDir := t.TempDir()
	persistPath := filepath.Join(tmpDir, "os_sessions.json")

	store1 := NewSessionStore()
	if err := store1.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed on %s: %v", defaultSS.Name(), err)
	}
	masterSecret := "os-dpapi-master-secret-32-bytes-ok!"
	rawToken := "os-dpapi-raw-token-secret-32-bytes!"
	if _, err := store1.RegisterSession("sess-os-1", "dev-os-1", rawToken, masterSecret, time.Now().UTC().Add(time.Hour), NewScopedSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}

	diskBytes, err := os.ReadFile(persistPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(diskBytes), masterSecret) || strings.Contains(string(diskBytes), rawToken) {
		t.Fatalf("persisted JSON leaked plaintext secrets")
	}

	store2 := NewSessionStore()
	if err := store2.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence store2 failed: %v", err)
	}
	if err := store2.LoadPersistedSessions(); err != nil {
		t.Fatalf("LoadPersistedSessions failed: %v", err)
	}
	rec, err := store2.AuthenticateBearer(rawToken, "sess-os-1", "dev-os-1")
	if err != nil {
		t.Fatalf("AuthenticateBearer after OS restart failed: %v", err)
	}
	if rec.IsAuthorized(PermissionWorkspaceList) {
		t.Fatalf("empty-permission OS persisted session escalated permissions")
	}
}

func TestSessionStore_FailClosedOnCorruptLegacyAndMissingPermissions(t *testing.T) {
	mockSS := newMockSecretStore(true)
	_, _ = mockSS.Store("http_session", "sess-missing-perms", []byte("secret-value-32-bytes-0123456789!"))

	cases := []struct {
		name    string
		content string
	}{
		{"empty_file", ""},
		{"truncated_json", `{"version": 2, "sessions": [`},
		{"legacy_plaintext_array", fmt.Sprintf(`[{"session_id":"sess-legacy","device_id":"dev-1","token_hash":%q,"session_secret":"plaintext-secret","expires_at":"2099-01-01T00:00:00Z"}]`, HashSessionToken("tok-legacy"))},
		{"unsupported_version", `{"version": 1, "sessions": []}`},
		{"missing_permissions_field", fmt.Sprintf(`{"version": 2, "sessions": [{"session_id":"sess-missing-perms","device_id":"dev-1","token_hash":%q,"secret_ref":"mock-ref:http_session:sess-missing-perms","expires_at":"2099-01-01T00:00:00Z"}]}`, HashSessionToken("tok-missing"))},
		{"null_permissions_field", fmt.Sprintf(`{"version": 2, "sessions": [{"session_id":"sess-missing-perms","device_id":"dev-1","token_hash":%q,"secret_ref":"mock-ref:http_session:sess-missing-perms","permissions":null,"expires_at":"2099-01-01T00:00:00Z"}]}`, HashSessionToken("tok-null"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sessions.json")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatalf("write test fixture failed: %v", err)
			}
			store := NewSessionStore()
			store.SetSecretStore(mockSS)
			_ = store.EnablePersistence(path)
			err := store.LoadPersistedSessions()
			if err == nil || !errors.Is(err, ErrCorruptPersistedState) {
				t.Fatalf("expected ErrCorruptPersistedState, got: %v", err)
			}
			if _, authErr := store.AuthenticateBearer("tok-legacy", "sess-legacy", "dev-1"); authErr != ErrSessionNotFound {
				t.Fatalf("legacy session must not be restored, got: %v", authErr)
			}
			if _, authErr := store.AuthenticateBearer("tok-missing", "sess-missing-perms", "dev-1"); authErr != ErrSessionNotFound {
				t.Fatalf("missing/null-permissions session must not be restored, got: %v", authErr)
			}
		})
	}
}

func TestSessionStore_RevocationSurvivesRestartAndCannotResurrect(t *testing.T) {
	tmpDir := t.TempDir()
	persistPath := filepath.Join(tmpDir, "sessions.json")
	mockSS := newMockSecretStore(true)

	store1 := NewSessionStore()
	store1.SetSecretStore(mockSS)
	if err := store1.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}

	tok1 := "tok-rev-restart-01"
	tok2 := "tok-rev-restart-02"
	_, _ = store1.RegisterSession("sess-r1", "dev-r1", tok1, "master-r1-32-bytes-01234567890!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())
	_, _ = store1.RegisterSession("sess-r2", "dev-r2", tok2, "master-r2-32-bytes-01234567890!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())

	if ok, err := store1.RevokeSession("sess-r1"); err != nil || !ok {
		t.Fatalf("RevokeSession failed: ok=%v err=%v", ok, err)
	}
	if count, err := store1.RevokeDevice("dev-r2"); err != nil || count != 1 {
		t.Fatalf("RevokeDevice failed: count=%d err=%v", count, err)
	}

	// Simulate restart against same persisted file + mock secret store
	store2 := NewSessionStore()
	store2.SetSecretStore(mockSS)
	_ = store2.EnablePersistence(persistPath)
	if err := store2.LoadPersistedSessions(); err != nil {
		t.Fatalf("LoadPersistedSessions after revocation failed: %v", err)
	}

	if _, err := store2.AuthenticateBearer(tok1, "sess-r1", "dev-r1"); err != ErrSessionRevoked {
		t.Fatalf("revoked session resurrected after restart! got: %v", err)
	}
	if _, err := store2.GetActiveSession("sess-r2", "dev-r2"); err != ErrSessionRevoked {
		t.Fatalf("revoked device resurrected after restart! got: %v", err)
	}

	// Even if a stale entry for sess-r1 remained in sessions alongside revoked_sessions tombstone,
	// LoadPersistedSessions must still reject it.
	var fileObj persistedSessionStoreFile
	raw, _ := os.ReadFile(persistPath)
	_ = json.Unmarshal(raw, &fileObj)
	_, _ = mockSS.Store("http_session", "sess-r1", []byte("stale-secret-32-bytes-0123456789!"))
	fileObj.Sessions = append(fileObj.Sessions, PersistedSessionEntry{
		SessionID:   "sess-r1",
		DeviceID:    "dev-r1",
		TokenHash:   HashSessionToken(tok1),
		SecretRef:   "mock-ref:http_session:sess-r1",
		Permissions: NewFullSessionPermissions().Granted,
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	})
	tampered, _ := json.Marshal(fileObj)
	_ = os.WriteFile(persistPath, tampered, 0600)

	store3 := NewSessionStore()
	store3.SetSecretStore(mockSS)
	_ = store3.EnablePersistence(persistPath)
	_ = store3.LoadPersistedSessions()
	if _, err := store3.AuthenticateBearer(tok1, "sess-r1", "dev-r1"); err != ErrSessionRevoked {
		t.Fatalf("tombstone failed to block stale persisted entry on restart: %v", err)
	}
}

func TestSessionStore_PersistenceFailuresAndUnavailableSecureStore(t *testing.T) {
	t.Run("persistence failure during registration preserves in-memory session and exposes error", func(t *testing.T) {
		tmpDir := t.TempDir()
		persistPath := filepath.Join(tmpDir, "sessions.json")
		mockSS := newMockSecretStore(true)

		store1 := NewSessionStore()
		store1.SetSecretStore(mockSS)
		if err := store1.EnablePersistence(persistPath); err != nil {
			t.Fatalf("EnablePersistence failed: %v", err)
		}

		// Inject failure in SecretStore.Store
		mockSS.storeErr = errors.New("keyring write refused")
		rec, err := store1.RegisterSession("sess-reg-fail", "dev-rf", "tok-rf", "master-rf-32-bytes-0123456789!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())
		if !errors.Is(err, ErrPersistenceFailed) {
			t.Fatalf("expected ErrPersistenceFailed on registration failure, got: %v", err)
		}
		if rec == nil {
			t.Fatalf("expected in-memory record to remain usable when persistence fails")
		}
		if _, authErr := store1.AuthenticateBearer("tok-rf", "sess-reg-fail", "dev-rf"); authErr != nil {
			t.Fatalf("in-memory authentication should still succeed after persistence error, got: %v", authErr)
		}

		// Restart -> unpersisted session must NOT exist
		mockSS.storeErr = nil
		store2 := NewSessionStore()
		store2.SetSecretStore(mockSS)
		_ = store2.EnablePersistence(persistPath)
		_ = store2.LoadPersistedSessions()
		if _, authErr := store2.AuthenticateBearer("tok-rf", "sess-reg-fail", "dev-rf"); authErr != ErrSessionNotFound {
			t.Fatalf("expected ErrSessionNotFound after failed persistence restart, got: %v", authErr)
		}
	})

	t.Run("persistence failure during revocation revokes in-memory and returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		persistPath := filepath.Join(tmpDir, "sessions.json")
		mockSS := newMockSecretStore(true)

		store := NewSessionStore()
		store.SetSecretStore(mockSS)
		if err := store.EnablePersistence(persistPath); err != nil {
			t.Fatalf("EnablePersistence failed: %v", err)
		}
		_, err := store.RegisterSession("sess-rev-fail", "dev-rv", "tok-rv", "master-rv-32-bytes-0123456789!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())
		if err != nil {
			t.Fatalf("RegisterSession failed: %v", err)
		}

		mockSS.deleteErr = errors.New("keyring delete IO error")
		revoked, err := store.RevokeSession("sess-rev-fail")
		if !revoked || !errors.Is(err, ErrPersistenceFailed) {
			t.Fatalf("expected (true, ErrPersistenceFailed), got (%v, %v)", revoked, err)
		}
		if _, authErr := store.AuthenticateBearer("tok-rv", "sess-rev-fail", "dev-rv"); authErr != ErrSessionRevoked {
			t.Fatalf("in-memory session MUST still be revoked immediately when persistence fails, got: %v", authErr)
		}

		// Also verify disk write failure (e.g. target path replaced by directory) returns ErrPersistenceFailed
		badPath := filepath.Join(tmpDir, "bad_dir_target")
		_ = os.MkdirAll(badPath, 0700)
		storeBad := NewSessionStore()
		storeBad.SetSecretStore(mockSS)
		mockSS.deleteErr = nil
		_ = storeBad.EnablePersistence(badPath)
		_, _ = storeBad.RegisterSession("sess-disk-fail", "dev-df", "tok-df", "master-df-32-bytes-0123456789!", time.Now().UTC().Add(time.Hour), nil)
		_, revDiskErr := storeBad.RevokeDevice("dev-df")
		if !errors.Is(revDiskErr, ErrPersistenceFailed) {
			t.Fatalf("expected ErrPersistenceFailed when atomic file replacement fails, got: %v", revDiskErr)
		}
		if _, authErr := storeBad.GetActiveSession("sess-disk-fail", "dev-df"); authErr != ErrSessionRevoked {
			t.Fatalf("in-memory device revocation MUST still succeed on disk failure, got: %v", authErr)
		}
	})

	t.Run("unavailable secure store refuses plaintext persistence and requires re-pairing after restart", func(t *testing.T) {
		tmpDir := t.TempDir()
		persistPath := filepath.Join(tmpDir, "unavail_sessions.json")
		unavailSS := newMockSecretStore(false)

		store1 := NewSessionStore()
		store1.SetSecretStore(unavailSS)
		if err := store1.EnablePersistence(persistPath); !errors.Is(err, ErrSecureStoreUnavailable) {
			t.Fatalf("expected ErrSecureStoreUnavailable from EnablePersistence, got: %v", err)
		}

		masterSecret := "unavail-store-master-secret-32b!"
		rawToken := "unavail-store-raw-token-32b!"
		rec, err := store1.RegisterSession("sess-unavail", "dev-unavail", rawToken, masterSecret, time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())
		if err != nil || rec == nil {
			t.Fatalf("expected memory-only registration when store configured in unavailable tombstone-only mode, got rec=%v err=%v", rec, err)
		}

		rawBytes, err := os.ReadFile(persistPath)
		if err != nil {
			t.Fatalf("expected tombstone state file write: %v", err)
		}
		if strings.Contains(string(rawBytes), masterSecret) || strings.Contains(string(rawBytes), rawToken) {
			t.Fatalf("SECURITY FAILURE: plaintext secret written despite unavailable secure store!")
		}

		// Revocation tombstone still persists cleanly even when secure store is unavailable
		if ok, err := store1.RevokeSession("sess-unavail"); err != nil || !ok {
			t.Fatalf("expected tombstone-only revocation to persist cleanly: ok=%v err=%v", ok, err)
		}

		store2 := NewSessionStore()
		store2.SetSecretStore(unavailSS)
		_ = store2.EnablePersistence(persistPath)
		if err := store2.LoadPersistedSessions(); err != nil {
			t.Fatalf("LoadPersistedSessions failed: %v", err)
		}
		// Non-revoked session registered in memory-only mode is NOT recoverable on restart,
		// and revoked session remains revoked.
		if _, err := store2.GetActiveSession("sess-unavail", "dev-unavail"); err != ErrSessionRevoked {
			t.Fatalf("expected ErrSessionRevoked across restart in tombstone-only mode, got: %v", err)
		}
	})
}

func TestSessionStore_AbsentSessionRevocationTombstoneAndRollback(t *testing.T) {
	t.Run("revoking absent session persists durable tombstone across restarts and resists stale reload", func(t *testing.T) {
		tmpDir := t.TempDir()
		persistPath := filepath.Join(tmpDir, "sessions.json")
		mockSS := newMockSecretStore(true)

		// 1. store1 creates a valid session and saves JSON
		store1 := NewSessionStore()
		store1.SetSecretStore(mockSS)
		if err := store1.EnablePersistence(persistPath); err != nil {
			t.Fatalf("EnablePersistence store1 failed: %v", err)
		}
		tok := "tok-absent-rev-32bytes-01234567!"
		if _, err := store1.RegisterSession("sess-abs-01", "dev-abs-01", tok, "master-abs-01-32bytes-012345678!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
			t.Fatalf("RegisterSession failed: %v", err)
		}
		staleJSON, err := os.ReadFile(persistPath)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}

		// 2. store2 loads with OS secret store UNAVAILABLE -> sess-abs-01 fails to load and is absent from memory
		mockSS.available = false
		store2 := NewSessionStore()
		store2.SetSecretStore(mockSS)
		if err := store2.EnablePersistence(persistPath); !errors.Is(err, ErrSecureStoreUnavailable) {
			t.Fatalf("expected ErrSecureStoreUnavailable, got: %v", err)
		}
		loadErr := store2.LoadPersistedSessions()
		if !errors.Is(loadErr, ErrSecureStoreUnavailable) {
			t.Fatalf("expected ErrSecureStoreUnavailable during LoadPersistedSessions, got: %v", loadErr)
		}

		// 3. Revoke absent session -> returns (false, nil) meaning absent from memory, tombstone persisted
		wasActive, err := store2.RevokeSession("sess-abs-01")
		if err != nil {
			t.Fatalf("expected tombstone persistence to succeed for absent session, got err: %v", err)
		}
		if wasActive {
			t.Fatalf("expected wasActive=false for session absent from memory")
		}

		// Repeating revoke is idempotent and stays revoked
		wasActiveRepeat, errRepeat := store2.RevokeSession("sess-abs-01")
		if errRepeat != nil || wasActiveRepeat {
			t.Fatalf("expected idempotent repeat revoke (false, nil), got (%v, %v)", wasActiveRepeat, errRepeat)
		}

		// 4. Restore secret store availability and restart into store3 -> session must remain revoked
		mockSS.available = true
		store3 := NewSessionStore()
		store3.SetSecretStore(mockSS)
		if err := store3.EnablePersistence(persistPath); err != nil {
			t.Fatalf("EnablePersistence store3 failed: %v", err)
		}
		if err := store3.LoadPersistedSessions(); err != nil {
			t.Fatalf("LoadPersistedSessions store3 failed: %v", err)
		}
		if _, authErr := store3.AuthenticateBearer(tok, "sess-abs-01", "dev-abs-01"); authErr != ErrSessionRevoked {
			t.Fatalf("expected ErrSessionRevoked after restart with tombstone, got: %v", authErr)
		}

		// 5. Even if an attacker/backup restores the original stale JSON without tombstones and reloads store3,
		//    existing tombstone in memory must NOT be overridden.
		if err := os.WriteFile(persistPath, staleJSON, 0600); err != nil {
			t.Fatalf("WriteFile staleJSON failed: %v", err)
		}
		_ = store3.LoadPersistedSessions()
		if _, authErr := store3.AuthenticateBearer(tok, "sess-abs-01", "dev-abs-01"); authErr != ErrSessionRevoked {
			t.Fatalf("stale JSON overrode active tombstone in store3! got: %v", authErr)
		}
	})

	t.Run("revoking empty sessionID returns error and tombstone write failure returns ErrPersistenceFailed", func(t *testing.T) {
		store := NewSessionStore()
		if _, err := store.RevokeSession(""); err == nil {
			t.Fatalf("expected error for empty sessionID")
		}

		badPath := filepath.Join(t.TempDir(), "dir_blocking_tombstone")
		_ = os.MkdirAll(badPath, 0700)
		storeFail := NewSessionStore()
		storeFail.SetSecretStore(newMockSecretStore(true))
		_ = storeFail.EnablePersistence(badPath)
		wasActive, err := storeFail.RevokeSession("sess-absent-write-fail")
		if wasActive || !errors.Is(err, ErrPersistenceFailed) {
			t.Fatalf("expected (false, ErrPersistenceFailed) when tombstone write fails, got (%v, %v)", wasActive, err)
		}
	})

	t.Run("RemoveSession records tombstone and propagates delete/write failures", func(t *testing.T) {
		tmpDir := t.TempDir()
		persistPath := filepath.Join(tmpDir, "sessions.json")
		mockSS := newMockSecretStore(true)

		store := NewSessionStore()
		store.SetSecretStore(mockSS)
		_ = store.EnablePersistence(persistPath)
		_, _ = store.RegisterSession("sess-rb-1", "dev-rb", "tok-rb-1", "master-rb-32-bytes-01234567890!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())

		if err := store.RemoveSession("sess-rb-1"); err != nil {
			t.Fatalf("RemoveSession failed: %v", err)
		}

		// Restart -> rolled-back session cannot be resurrected or re-registered
		store2 := NewSessionStore()
		store2.SetSecretStore(mockSS)
		_ = store2.EnablePersistence(persistPath)
		_ = store2.LoadPersistedSessions()
		if _, err := store2.AuthenticateBearer("tok-rb-1", "sess-rb-1", "dev-rb"); err != ErrSessionRevoked {
			t.Fatalf("expected ErrSessionRevoked for rolled-back session after restart, got: %v", err)
		}

		// Delete error during RemoveSession must propagate
		store3 := NewSessionStore()
		store3.SetSecretStore(mockSS)
		_ = store3.EnablePersistence(filepath.Join(tmpDir, "sessions3.json"))
		_, _ = store3.RegisterSession("sess-rb-err", "dev-rb", "tok-rb-err", "master-rb-err-32bytes-012345678!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions())
		mockSS.deleteErr = errors.New("secret delete permission denied")
		if err := store3.RemoveSession("sess-rb-err"); !errors.Is(err, ErrPersistenceFailed) {
			t.Fatalf("expected ErrPersistenceFailed on RemoveSession delete error, got: %v", err)
		}
	})
}

// TestSessionStore_AbsentRevocationDestroysPersistedSealedSecret proves that revoking a session that is
// absent from memory still destroys the sealed secret referenced only by persisted state. Without this,
// a failed atomic JSON replacement could leave a recoverable ciphertext reference on disk.
func TestSessionStore_AbsentRevocationDestroysPersistedSealedSecret(t *testing.T) {
	tmpDir := t.TempDir()
	persistPath := filepath.Join(tmpDir, "sessions.json")
	mockSS := newMockSecretStore(true)

	store1 := NewSessionStore()
	store1.SetSecretStore(mockSS)
	if err := store1.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}
	tokOne := "tok-absorb-1-32bytes-0123456789ab"
	tokTwo := "tok-absorb-2-32bytes-0123456789ab"
	if _, err := store1.RegisterSession("sess-absorb-1", "dev-absorb-1", tokOne, "master-absorb-1-32bytes-0123456!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession sess-absorb-1 failed: %v", err)
	}
	if _, err := store1.RegisterSession("sess-absorb-2", "dev-absorb-2", tokTwo, "master-absorb-2-32bytes-0123456!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession sess-absorb-2 failed: %v", err)
	}
	for _, id := range []string{"http_session:sess-absorb-1", "http_session:sess-absorb-2"} {
		if _, ok := mockSS.secrets[id]; !ok {
			t.Fatalf("expected sealed secret %s to exist before revocation", id)
		}
	}

	staleJSON, err := os.ReadFile(persistPath)
	if err != nil {
		t.Fatalf("ReadFile stale snapshot failed: %v", err)
	}

	// store2 never loads persisted sessions: both records are absent from memory but still referenced on disk.
	store2 := NewSessionStore()
	store2.SetSecretStore(mockSS)
	if err := store2.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence store2 failed: %v", err)
	}

	wasActive, revErr := store2.RevokeSession("sess-absorb-1")
	if revErr != nil || wasActive {
		t.Fatalf("expected (false, nil) for absent session revocation, got (%v, %v)", wasActive, revErr)
	}
	if _, stillPresent := mockSS.secrets["http_session:sess-absorb-1"]; stillPresent {
		t.Fatalf("revoking an unloaded session must destroy its persisted sealed secret")
	}

	// Device-wide revocation must likewise destroy sealed secrets of unloaded sessions on that device.
	if _, devErr := store2.RevokeDevice("dev-absorb-2"); devErr != nil {
		t.Fatalf("RevokeDevice failed: %v", devErr)
	}
	if _, stillPresent := mockSS.secrets["http_session:sess-absorb-2"]; stillPresent {
		t.Fatalf("device revocation must destroy the persisted sealed secret of an unloaded session")
	}

	// Replaying the pre-revocation JSON snapshot must not restore either session.
	if err := os.WriteFile(persistPath, staleJSON, 0600); err != nil {
		t.Fatalf("WriteFile staleJSON failed: %v", err)
	}
	store3 := NewSessionStore()
	store3.SetSecretStore(mockSS)
	if err := store3.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence store3 failed: %v", err)
	}
	if loadErr := store3.LoadPersistedSessions(); loadErr == nil {
		t.Fatalf("expected persisted reload to fail closed once sealed secrets were destroyed")
	}
	if _, authErr := store3.AuthenticateBearer(tokOne, "sess-absorb-1", "dev-absorb-1"); authErr != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound for revoked session restored from stale JSON, got: %v", authErr)
	}
	if _, authErr := store3.AuthenticateBearer(tokTwo, "sess-absorb-2", "dev-absorb-2"); authErr != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound for device-revoked session restored from stale JSON, got: %v", authErr)
	}
}

// TestSessionStore_RevocationPreservesUnloadedPersistedSessions proves that revoking (or rolling back)
// a single session while other durable sessions are not loaded into memory cannot silently wipe the rest
// of the persisted session set.
func TestSessionStore_RevocationPreservesUnloadedPersistedSessions(t *testing.T) {
	persistPath := filepath.Join(t.TempDir(), "sessions.json")
	mockSS := newMockSecretStore(true)

	store1 := NewSessionStore()
	store1.SetSecretStore(mockSS)
	if err := store1.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}
	keepTok := "tok-keep-unloaded-32bytes-012345"
	delTok := "tok-del-unloaded-32bytes-0123456"
	if _, err := store1.RegisterSession("sess-keep", "dev-keep", keepTok, "master-keep-unloaded-32bytes-012!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession keep failed: %v", err)
	}
	if _, err := store1.RegisterSession("sess-del", "dev-del", delTok, "master-del-unloaded-32bytes-0123!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession del failed: %v", err)
	}

	// A store that never loaded persisted sessions revokes and rolls back individual sessions.
	store2 := NewSessionStore()
	store2.SetSecretStore(mockSS)
	if err := store2.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence store2 failed: %v", err)
	}
	if _, err := store2.RevokeSession("sess-del"); err != nil {
		t.Fatalf("RevokeSession sess-del failed: %v", err)
	}

	// Restart with secure-store access restored: the untouched session must still be restored.
	store3 := NewSessionStore()
	store3.SetSecretStore(mockSS)
	if err := store3.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence store3 failed: %v", err)
	}
	if err := store3.LoadPersistedSessions(); err != nil {
		t.Fatalf("LoadPersistedSessions store3 failed: %v", err)
	}
	if _, err := store3.AuthenticateBearer(keepTok, "sess-keep", "dev-keep"); err != nil {
		t.Fatalf("unrelated persisted session must survive an unloaded revocation, got: %v", err)
	}
	if _, err := store3.AuthenticateBearer(delTok, "sess-del", "dev-del"); err != ErrSessionRevoked {
		t.Fatalf("expected revoke tombstone to block sess-del after restart, got: %v", err)
	}

	// RemoveSession (pairing rollback) must behave the same way for unloaded siblings.
	if err := store3.RemoveSession("sess-keep"); err != nil {
		t.Fatalf("RemoveSession sess-keep failed: %v", err)
	}
	store4 := NewSessionStore()
	store4.SetSecretStore(mockSS)
	_ = store4.EnablePersistence(persistPath)
	if err := store4.LoadPersistedSessions(); err != nil {
		t.Fatalf("LoadPersistedSessions store4 failed: %v", err)
	}
	if _, err := store4.AuthenticateBearer(keepTok, "sess-keep", "dev-keep"); err != ErrSessionRevoked {
		t.Fatalf("expected rolled-back session to stay revoked after restart, got: %v", err)
	}
}
