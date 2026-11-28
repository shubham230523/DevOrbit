//go:build windows

package security

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsDPAPISecretStore_StoreLoadDeleteAndFailClosed(t *testing.T) {
	secretsDir := filepath.Join(t.TempDir(), "secrets")
	ss := NewDPAPISecretStore(secretsDir)

	if !ss.Available() || ss.Name() != "windows-dpapi-file-v2" {
		t.Fatalf("unexpected Windows DPAPI store metadata: avail=%v name=%s", ss.Available(), ss.Name())
	}

	secret := []byte("windows-dpapi-master-secret-32-bytes!")
	ref, err := ss.Store("http_session", "sess-win-01", secret)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if !strings.HasPrefix(ref, dpapiFileRefPrefix) {
		t.Fatalf("expected %s prefix in ref, got %q", dpapiFileRefPrefix, ref)
	}
	if strings.Contains(ref, string(secret)) {
		t.Fatalf("ref must never expose plaintext secret")
	}

	// Verify file on disk does not contain plaintext secret
	matches, _ := filepath.Glob(filepath.Join(secretsDir, "*.dpapi"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 DPAPI secret file, got %d", len(matches))
	}
	diskBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile DPAPI file failed: %v", err)
	}
	if strings.Contains(string(diskBytes), string(secret)) {
		t.Fatalf("DPAPI file on disk leaked plaintext secret!")
	}

	// Load succeeds with matching scope + id + ref
	loaded, err := ss.Load("http_session", "sess-win-01", ref)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if string(loaded) != string(secret) {
		t.Fatalf("loaded secret mismatch")
	}

	// Mismatched session ID (entropy mismatch) must fail
	if _, err := ss.Load("http_session", "sess-wrong-id", ref); err == nil {
		t.Fatalf("expected CryptUnprotectData to reject mismatched session id entropy")
	}

	// Legacy inline dpapi-v1 refs must fail closed
	if _, err := ss.Load("http_session", "sess-win-01", "dpapi-v1:AAAA"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound for legacy inline dpapi-v1 ref, got: %v", err)
	}

	// Delete removes the external secret file so Load fails closed
	if err := ss.Delete("http_session", "sess-win-01", ref); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := ss.Load("http_session", "sess-win-01", ref); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound after Delete, got: %v", err)
	}
	// Repeating Delete is idempotent
	if err := ss.Delete("http_session", "sess-win-01", ref); err != nil {
		t.Fatalf("idempotent Delete failed: %v", err)
	}
}

func TestWindowsDPAPISecretStore_RevokeAndRollbackBlockStaleFileRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	persistPath := filepath.Join(tmpDir, "sessions.json")
	dpapiDir := filepath.Join(tmpDir, "dpapi_secrets")
	ss := NewDPAPISecretStore(dpapiDir)

	// 1. Register sess-rev and sess-rb with Windows DPAPI store
	store1 := NewSessionStore()
	store1.SetSecretStore(ss)
	if err := store1.EnablePersistence(persistPath); err != nil {
		t.Fatalf("EnablePersistence failed: %v", err)
	}

	tokRev := "tok-win-rev-32bytes-0123456789ab"
	tokRB := "tok-win-rb-32bytes-0123456789abc"
	if _, err := store1.RegisterSession("sess-win-rev", "dev-1", tokRev, "sec-win-rev-32bytes-0123456789!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession rev failed: %v", err)
	}
	if _, err := store1.RegisterSession("sess-win-rb", "dev-2", tokRB, "sec-win-rb-32bytes-0123456789!!", time.Now().UTC().Add(time.Hour), NewFullSessionPermissions()); err != nil {
		t.Fatalf("RegisterSession rb failed: %v", err)
	}

	// Capture stale JSON snapshot BEFORE revoking/rolling back
	staleSnapshot, err := os.ReadFile(persistPath)
	if err != nil {
		t.Fatalf("ReadFile stale snapshot failed: %v", err)
	}

	// Revoke sess-win-rev & rollback sess-win-rb (both delete their .dpapi secret files)
	if ok, err := store1.RevokeSession("sess-win-rev"); err != nil || !ok {
		t.Fatalf("RevokeSession failed: ok=%v err=%v", ok, err)
	}
	if err := store1.RemoveSession("sess-win-rb"); err != nil {
		t.Fatalf("RemoveSession failed: %v", err)
	}

	// Simulate an attacker / failed atomic JSON overwrite leaving the stale JSON snapshot on disk.
	// Because the .dpapi secret files were deleted, restart MUST NOT restore either session!
	if err := os.WriteFile(persistPath, staleSnapshot, 0600); err != nil {
		t.Fatalf("restore stale snapshot failed: %v", err)
	}

	store2 := NewSessionStore()
	store2.SetSecretStore(ss)
	_ = store2.EnablePersistence(persistPath)
	err = store2.LoadPersistedSessions()
	if err == nil {
		t.Fatalf("expected LoadPersistedSessions to fail loading deleted DPAPI secrets from stale JSON")
	}
	if _, authErr := store2.AuthenticateBearer(tokRev, "sess-win-rev", "dev-1"); authErr != ErrSessionNotFound {
		t.Fatalf("deleted revoked secret must not authenticate after restart, got: %v", authErr)
	}
	if _, authErr := store2.AuthenticateBearer(tokRB, "sess-win-rb", "dev-2"); authErr != ErrSessionNotFound {
		t.Fatalf("deleted rolled-back secret must not authenticate after restart, got: %v", authErr)
	}
}

func TestWindowsDPAPISecretStore_ErrorPropagationAndFailClosed(t *testing.T) {
	tmpDir := t.TempDir()

	// A directory path that cannot be created (its parent is a regular file) must surface a store error.
	blocker := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blocker, []byte("not-a-directory"), 0600); err != nil {
		t.Fatalf("WriteFile blocker failed: %v", err)
	}
	brokenStore := NewDPAPISecretStore(filepath.Join(blocker, "secrets"))
	if _, err := brokenStore.Store("http_session", "sess-broken", []byte("secret-material-32-bytes-0123456789")); err == nil {
		t.Fatalf("expected DPAPI store to propagate a filesystem error when the secrets directory cannot be created")
	}

	ss := NewDPAPISecretStore(filepath.Join(tmpDir, "ok_secrets"))

	// Scope values are used as a filename component and must reject path traversal / separators.
	for _, badScope := range []string{"", "../escape", "..\\escape", "a/b", "a.b"} {
		if _, err := ss.Store(badScope, "sess-scope", []byte("secret-material-32-bytes-0123456789")); err == nil {
			t.Fatalf("expected Store to reject unsafe scope %q", badScope)
		}
	}

	// References must be opaque 32-char lowercase hex identifiers; anything else fails closed.
	for _, badRef := range []string{
		"",
		"dpapi-file-v2:",
		"dpapi-file-v2:../../etc/passwd",
		"dpapi-file-v2:ABCDEF0123456789ABCDEF01234567",
		"dpapi-v1:AAAA",
	} {
		if _, err := ss.Load("http_session", "sess-ref", badRef); !errors.Is(err, ErrSecretNotFound) {
			t.Fatalf("expected ErrSecretNotFound for malformed ref %q, got: %v", badRef, err)
		}
	}

	// A syntactically valid but unknown reference fails closed.
	if _, err := ss.Load("http_session", "sess-ref", dpapiFileRefPrefix+"0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound for unknown DPAPI reference, got: %v", err)
	}

	// Corrupt ciphertext on disk must surface an error rather than returning plaintext.
	ref, err := ss.Store("http_session", "sess-corrupt", []byte("secret-material-32-bytes-0123456789"))
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	corruptPath := filepath.Join(tmpDir, "ok_secrets", "http_session_"+strings.TrimPrefix(ref, dpapiFileRefPrefix)+".dpapi")
	if err := os.WriteFile(corruptPath, []byte("corrupted-dpapi-ciphertext"), 0600); err != nil {
		t.Fatalf("WriteFile corrupt ciphertext failed: %v", err)
	}
	if _, err := ss.Load("http_session", "sess-corrupt", ref); err == nil || errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected a CryptUnprotectData failure for corrupt ciphertext, got: %v", err)
	}
}
