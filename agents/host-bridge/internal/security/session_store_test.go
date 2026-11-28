package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	count := store.RevokeDevice("dev-1")
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
	if !store.RevokeSession("sess-3") {
		t.Fatalf("expected RevokeSession(sess-3) to return true")
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

func TestSessionStore_PersistenceRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	persistPath := filepath.Join(tmpDir, "sessions.json")

	store1 := NewSessionStore()
	store1.EnablePersistence(persistPath)
	rawToken := "super-secret-raw-bearer-token-value"
	_, err := store1.RegisterSession(
		"sess-persist",
		"dev-persist",
		rawToken,
		"master-secret-32-bytes-01234567",
		time.Now().UTC().Add(1*time.Hour),
		NewScopedSessionPermissions(PermissionWorkspaceList, PermissionFileRead),
	)
	if err != nil {
		t.Fatalf("RegisterSession failed: %v", err)
	}

	rawBytes, err := os.ReadFile(persistPath)
	if err != nil {
		t.Fatalf("failed reading persisted session file: %v", err)
	}
	if strings.Contains(string(rawBytes), rawToken) {
		t.Fatalf("persisted session file must NEVER contain raw bearer token")
	}

	store2 := NewSessionStore()
	store2.EnablePersistence(persistPath)
	store2.LoadPersistedSessions()

	rec2, err := store2.AuthenticateBearer(rawToken, "sess-persist", "dev-persist")
	if err != nil {
		t.Fatalf("AuthenticateBearer after persistence load failed: %v", err)
	}
	if !rec2.IsAuthorized(PermissionFileRead) || rec2.IsAuthorized(PermissionFileWrite) {
		t.Fatalf("expected persisted scoped permissions to survive reload")
	}
}
