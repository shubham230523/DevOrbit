package security

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrSessionNotFound        = errors.New("session not found or invalid credentials")
	ErrSessionExpired         = errors.New("session has expired")
	ErrSessionRevoked         = errors.New("session or device has been revoked")
	ErrSessionBindingMismatch = errors.New("session or device identity binding mismatch")
	ErrReplayDetected         = errors.New("replayed or stale request rejected")
	ErrInvalidTimestamp       = errors.New("invalid or out-of-window request timestamp")
	ErrMissingBinding         = errors.New("session_id and device_id headers are required")
	ErrPersistenceFailed      = errors.New("session persistence failed")
	ErrCorruptPersistedState  = errors.New("corrupt or incompatible persisted session state")
)

const (
	MaxClockSkewPast      = 5 * time.Minute
	MaxClockSkewFuture    = 1 * time.Minute
	NonceRetentionTTL     = 10 * time.Minute
	persistedStoreVersion = 2
)

// SessionRecord holds the server-authoritative identity, credentials, permissions,
// and cryptographic state for a paired mobile session.
type SessionRecord struct {
	SessionID     string
	DeviceID      string
	TokenHash     string
	MasterSecret  string
	SecretRef     string
	ExpiresAt     time.Time
	Revoked       bool
	RevokedAt     time.Time
	Permissions   *SessionPermissions
	DirectHTTPE2E *E2ESession
	HTTPAuthKey   []byte

	mu         sync.Mutex
	seenNonces map[string]time.Time
}

// IsExpired reports whether the session or its E2E keys have expired.
func (r *SessionRecord) IsExpired() bool {
	if r == nil {
		return true
	}
	return time.Now().UTC().After(r.ExpiresAt.UTC())
}

// IsAuthorized checks whether the session is active and holds the requested permission.
func (r *SessionRecord) IsAuthorized(perm ActionPermission) bool {
	if r == nil || r.Revoked || r.IsExpired() {
		return false
	}
	if perm == "" {
		return true
	}
	return r.Permissions.IsAuthorized(perm)
}

func (r *SessionRecord) destroyKeysLocked() {
	if r.DirectHTTPE2E != nil {
		r.DirectHTTPE2E.Destroy()
	}
	for i := range r.HTTPAuthKey {
		r.HTTPAuthKey[i] = 0
	}
	r.MasterSecret = ""
	r.SecretRef = ""
	r.mu.Lock()
	r.seenNonces = make(map[string]time.Time)
	r.mu.Unlock()
}

// VerifyAndConsumeNonce validates timestamp freshness and ensures the nonce has not
// been used previously within the replay window for this session.
func (r *SessionRecord) VerifyAndConsumeNonce(nonce string, ts time.Time, now time.Time) error {
	if len(nonce) < 8 || len(nonce) > 128 {
		return ErrReplayDetected
	}
	if !IsTimestampFresh(ts, now) {
		return ErrInvalidTimestamp
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seenNonces == nil {
		r.seenNonces = make(map[string]time.Time)
	}

	// Prune expired nonces
	for k, exp := range r.seenNonces {
		if now.After(exp) {
			delete(r.seenNonces, k)
		}
	}

	if _, exists := r.seenNonces[nonce]; exists {
		return ErrReplayDetected
	}
	r.seenNonces[nonce] = now.Add(NonceRetentionTTL)
	return nil
}

// PersistedSessionEntry is the on-disk representation of an active session.
// Plaintext bearer tokens and plaintext session_secret values are NEVER persisted.
// Permissions intentionally omits `omitempty` so empty permission maps persist as `{}` rather than missing/nil.
type PersistedSessionEntry struct {
	SessionID   string                    `json:"session_id"`
	DeviceID    string                    `json:"device_id"`
	TokenHash   string                    `json:"token_hash"`
	SecretRef   string                    `json:"secret_ref,omitempty"`
	Permissions map[ActionPermission]bool `json:"permissions"`
	ExpiresAt   time.Time                 `json:"expires_at"`
}

type persistedSessionStoreFile struct {
	Version         int                     `json:"version"`
	Sessions        []PersistedSessionEntry `json:"sessions"`
	RevokedSessions map[string]time.Time    `json:"revoked_sessions,omitempty"`
	RevokedDevices  map[string]time.Time    `json:"revoked_devices,omitempty"`
}

// SessionStore manages active, expired, and revoked sessions across direct HTTP
// and Broker WebSocket transports.
type SessionStore struct {
	mu               sync.RWMutex
	sessions         map[string]*SessionRecord
	deviceSessions   map[string]map[string]bool
	revokedSessions  map[string]time.Time
	revokedDevices   map[string]time.Time
	storePath        string
	secretStore      SecretStore
	persistSecrets   bool
	onSessionUsed    func(sessionID string)
	onSessionRevoked func(sessionID, deviceID string)
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions:        make(map[string]*SessionRecord),
		deviceSessions:  make(map[string]map[string]bool),
		revokedSessions: make(map[string]time.Time),
		revokedDevices:  make(map[string]time.Time),
		secretStore:     NewDefaultSecretStore(),
	}
}

// SetSecretStore overrides the OS-backed secret store used for session persistence (primarily for testing).
func (s *SessionStore) SetSecretStore(ss SecretStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secretStore = ss
	if s.storePath != "" {
		s.persistSecrets = ss != nil && ss.Available()
	}
}

func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// WriteFileAtomic safely writes data to path using a same-directory temp file, fsync, 0600 permissions, and atomic rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// EnablePersistence configures durable atomic state storage at customPath (or ~/.devorbit/host_http_sessions.json).
// If no OS-backed confidential secret store is available, it enables tombstone-only persistence and returns
// ErrSecureStoreUnavailable so callers know recoverable session secrets will NOT be written to disk.
func (s *SessionStore) EnablePersistence(customPath string) error {
	path := customPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		path = filepath.Join(home, ".devorbit", "host_http_sessions.json")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("%w: %w", ErrPersistenceFailed, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.storePath = path
	if s.secretStore == nil || !s.secretStore.Available() {
		s.persistSecrets = false
		return ErrSecureStoreUnavailable
	}
	s.persistSecrets = true
	return nil
}

// readPersistedEntries parses the sealed session entries recorded in the persisted store file.
// A missing file yields no entries; malformed or incompatible payloads return an error.
func readPersistedEntries(path string) ([]PersistedSessionEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] == '[' {
		return nil, ErrCorruptPersistedState
	}
	var stored persistedSessionStoreFile
	if err := json.Unmarshal(trimmed, &stored); err != nil {
		return nil, err
	}
	if stored.Version != persistedStoreVersion {
		return nil, ErrCorruptPersistedState
	}
	return stored.Sessions, nil
}

// saveLocked atomically rewrites the persisted store. Entries for sessions that are known only to
// persisted state (never loaded by this process, e.g. because the OS secret store was unavailable at
// startup) are preserved unless they are tombstoned, device-revoked, or expired. Without this merge,
// a single revocation performed while sessions were unloaded would silently drop every other durable
// session and force every device to re-pair.
func (s *SessionStore) saveLocked() error {
	if s.storePath == "" {
		return nil
	}
	now := time.Now().UTC()
	entries := make([]PersistedSessionEntry, 0)
	if s.persistSecrets {
		if preserved, err := readPersistedEntries(s.storePath); err == nil {
			for _, e := range preserved {
				if e.SessionID == "" || e.DeviceID == "" || e.TokenHash == "" || e.SecretRef == "" {
					continue
				}
				if now.After(e.ExpiresAt) {
					continue
				}
				if _, revSess := s.revokedSessions[e.SessionID]; revSess {
					continue
				}
				if _, revDev := s.revokedDevices[e.DeviceID]; revDev {
					continue
				}
				if _, inMemory := s.sessions[e.SessionID]; inMemory {
					continue
				}
				entries = append(entries, e)
			}
		}
		for _, rec := range s.sessions {
			if rec.Revoked || now.After(rec.ExpiresAt) || rec.SecretRef == "" || rec.TokenHash == "" {
				continue
			}
			if _, revSess := s.revokedSessions[rec.SessionID]; revSess {
				continue
			}
			if _, revDev := s.revokedDevices[rec.DeviceID]; revDev {
				continue
			}
			granted := make(map[ActionPermission]bool)
			if rec.Permissions != nil && rec.Permissions.Granted != nil {
				for k, v := range rec.Permissions.Granted {
					granted[k] = v
				}
			}
			entries = append(entries, PersistedSessionEntry{
				SessionID:   rec.SessionID,
				DeviceID:    rec.DeviceID,
				TokenHash:   rec.TokenHash,
				SecretRef:   rec.SecretRef,
				Permissions: granted,
				ExpiresAt:   rec.ExpiresAt,
			})
		}
	}

	revSessCopy := make(map[string]time.Time, len(s.revokedSessions))
	for k, v := range s.revokedSessions {
		revSessCopy[k] = v
	}
	revDevCopy := make(map[string]time.Time, len(s.revokedDevices))
	for k, v := range s.revokedDevices {
		revDevCopy[k] = v
	}

	payload := persistedSessionStoreFile{
		Version:         persistedStoreVersion,
		Sessions:        entries,
		RevokedSessions: revSessCopy,
		RevokedDevices:  revDevCopy,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: marshal error: %w", ErrPersistenceFailed, err)
	}
	if err := WriteFileAtomic(s.storePath, data, 0600); err != nil {
		return fmt.Errorf("%w: %w", ErrPersistenceFailed, err)
	}
	return nil
}

// LoadPersistedSessions loads durable revocation tombstones and sealed sessions from disk.
// Fail-closed guarantees:
// - Legacy plaintext files (pre-v2 array format or containing plaintext session_secret) are rejected without restoring sessions.
// - Entries with nil/missing permissions are rejected (never elevated to full permissions).
// - Entries matching revoked_sessions or revoked_devices are rejected.
// - Corrupt or truncated JSON returns ErrCorruptPersistedState without partial restoration.
func (s *SessionStore) LoadPersistedSessions() error {
	s.mu.RLock()
	p := s.storePath
	ss := s.secretStore
	s.mu.RUnlock()

	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrPersistenceFailed, err)
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty session store file", ErrCorruptPersistedState)
	}
	if trimmed[0] == '[' {
		return fmt.Errorf("%w: legacy plaintext session file rejected; re-pairing required", ErrCorruptPersistedState)
	}

	var stored persistedSessionStoreFile
	if err := json.Unmarshal(trimmed, &stored); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptPersistedState, err)
	}
	if stored.Version != persistedStoreVersion {
		return fmt.Errorf("%w: unsupported persisted version %d", ErrCorruptPersistedState, stored.Version)
	}

	s.mu.Lock()
	for k, ts := range stored.RevokedSessions {
		if _, exists := s.revokedSessions[k]; !exists {
			s.revokedSessions[k] = ts
		}
		if rec, exists := s.sessions[k]; exists && !rec.Revoked {
			rec.Revoked = true
			rec.RevokedAt = ts
			rec.destroyKeysLocked()
		}
	}
	for k, ts := range stored.RevokedDevices {
		if _, exists := s.revokedDevices[k]; !exists {
			s.revokedDevices[k] = ts
		}
	}
	s.mu.Unlock()

	now := time.Now().UTC()
	var loadErrs []error
	for _, e := range stored.Sessions {
		if e.SessionID == "" || e.DeviceID == "" || e.TokenHash == "" || e.SecretRef == "" {
			loadErrs = append(loadErrs, fmt.Errorf("%w: incomplete session entry %q", ErrCorruptPersistedState, e.SessionID))
			continue
		}
		if now.After(e.ExpiresAt) {
			continue
		}

		s.mu.RLock()
		_, isRevSess := s.revokedSessions[e.SessionID]
		_, isRevDev := s.revokedDevices[e.DeviceID]
		s.mu.RUnlock()
		if isRevSess || isRevDev {
			continue
		}

		// Fail closed: missing/nil permissions must NEVER default to full permissions.
		if e.Permissions == nil {
			loadErrs = append(loadErrs, fmt.Errorf("%w: entry %q missing permissions map", ErrCorruptPersistedState, e.SessionID))
			continue
		}

		if ss == nil || !ss.Available() {
			loadErrs = append(loadErrs, fmt.Errorf("%w: cannot unseal persisted session %q", ErrSecureStoreUnavailable, e.SessionID))
			continue
		}
		secretBytes, err := ss.Load("http_session", e.SessionID, e.SecretRef)
		if err != nil || len(secretBytes) == 0 {
			loadErrs = append(loadErrs, fmt.Errorf("%w: failed loading secret for %q: %v", ErrCorruptPersistedState, e.SessionID, err))
			continue
		}
		masterSecret := string(secretBytes)
		zeroBytesSlice(secretBytes)

		grantedCopy := make(map[ActionPermission]bool, len(e.Permissions))
		for k, v := range e.Permissions {
			grantedCopy[k] = v
		}
		perms := &SessionPermissions{Granted: grantedCopy}

		if _, err := s.registerSessionInternal(e.SessionID, e.DeviceID, e.TokenHash, masterSecret, e.SecretRef, e.ExpiresAt, perms, false); err != nil {
			loadErrs = append(loadErrs, err)
		}
	}

	return errors.Join(loadErrs...)
}

func zeroBytesSlice(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// persistedSecretRef identifies a sealed per-session secret referenced by persisted state.
type persistedSecretRef struct {
	SessionID string
	SecretRef string
}

// persistedSecretRefsLocked returns the sealed secret references recorded in the persisted store
// file for entries matching keep. Callers must hold s.mu.
//
// Read failures are propagated so callers can report honestly that stale sealed secrets could not be
// removed. Unreadable/legacy payloads deliberately return no references and no error: saveLocked
// fully rewrites the file, so no persisted entry can reference those secrets afterwards.
func (s *SessionStore) persistedSecretRefsLocked(keep func(PersistedSessionEntry) bool) ([]persistedSecretRef, error) {
	if s.storePath == "" {
		return nil, nil
	}
	entries, err := readPersistedEntries(s.storePath)
	if err != nil {
		if errors.Is(err, ErrCorruptPersistedState) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrPersistenceFailed, err)
	}

	var refs []persistedSecretRef
	for _, e := range entries {
		if e.SessionID == "" || e.SecretRef == "" || !keep(e) {
			continue
		}
		refs = append(refs, persistedSecretRef{SessionID: e.SessionID, SecretRef: e.SecretRef})
	}
	return refs, nil
}

func clonePermissionsOrDefault(perms *SessionPermissions) *SessionPermissions {
	if perms == nil {
		return NewFullSessionPermissions()
	}
	granted := make(map[ActionPermission]bool, len(perms.Granted))
	for k, v := range perms.Granted {
		granted[k] = v
	}
	return &SessionPermissions{Granted: granted}
}

// RegisterSession provisions a new authenticated session with full validation and E2E key derivation.
// Passing nil perms intentionally defaults to NewFullSessionPermissions for new sessions; passing an
// explicitly empty SessionPermissions preserves zero privileges across both memory and persistence restarts.
// If persistence is enabled and fails, the session remains usable in memory while the persistence error is returned.
func (s *SessionStore) RegisterSession(sessionID, deviceID, sessionToken, masterSecret string, expiresAt time.Time, perms *SessionPermissions) (*SessionRecord, error) {
	if sessionID == "" || deviceID == "" || sessionToken == "" || masterSecret == "" {
		return nil, errors.New("session_id, device_id, session_token, and master_secret are required")
	}
	tokenHash := HashSessionToken(sessionToken)
	resolvedPerms := clonePermissionsOrDefault(perms)
	return s.registerSessionInternal(sessionID, deviceID, tokenHash, masterSecret, "", expiresAt, resolvedPerms, true)
}

func (s *SessionStore) registerSessionInternal(
	sessionID, deviceID, tokenHash, masterSecret, existingSecretRef string,
	expiresAt time.Time,
	perms *SessionPermissions,
	persistNow bool,
) (*SessionRecord, error) {
	expUTC := expiresAt.UTC()
	ttl := time.Until(expUTC)
	if ttl <= 0 {
		return nil, ErrSessionExpired
	}
	if perms == nil {
		perms = NewFullSessionPermissions()
	} else if perms.Granted == nil {
		perms = NewScopedSessionPermissions()
	}

	httpE2E, err := NewChannelE2ESession(ChannelDirectHTTP, sessionID, deviceID, masterSecret, ttl)
	if err != nil {
		return nil, fmt.Errorf("failed to derive direct HTTP E2E session: %w", err)
	}
	httpAuthKey, err := DeriveHTTPRequestAuthKey(masterSecret, sessionID, deviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive HTTP auth key: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, exists := s.sessions[sessionID]; exists {
		if existing.DeviceID != deviceID || subtle.ConstantTimeCompare([]byte(existing.TokenHash), []byte(tokenHash)) != 1 {
			return nil, errors.New("conflict: session_id already registered with different device or credentials")
		}
		return existing, nil
	}

	// Never allow re-registering an explicitly revoked sessionID.
	if _, revokedSess := s.revokedSessions[sessionID]; revokedSess {
		return nil, ErrSessionRevoked
	}

	// Re-pairing a previously revoked device issues a new sessionID and clears device revocation.
	delete(s.revokedDevices, deviceID)

	rec := &SessionRecord{
		SessionID:     sessionID,
		DeviceID:      deviceID,
		TokenHash:     tokenHash,
		MasterSecret:  masterSecret,
		SecretRef:     existingSecretRef,
		ExpiresAt:     expUTC,
		Permissions:   perms,
		DirectHTTPE2E: httpE2E,
		HTTPAuthKey:   httpAuthKey,
		seenNonces:    make(map[string]time.Time),
	}

	s.sessions[sessionID] = rec
	if _, ok := s.deviceSessions[deviceID]; !ok {
		s.deviceSessions[deviceID] = make(map[string]bool)
	}
	s.deviceSessions[deviceID][sessionID] = true

	if !persistNow || s.storePath == "" {
		return rec, nil
	}

	var storedNewSecret bool
	if s.persistSecrets {
		if s.secretStore == nil || !s.secretStore.Available() {
			// Secure store was previously available or injected as unavailable after EnablePersistence.
			// Never fall back to plaintext; keep in-memory session active and expose error to caller.
			_ = s.saveLocked()
			return rec, fmt.Errorf("%w: %w", ErrPersistenceFailed, ErrSecureStoreUnavailable)
		}
		ref, err := s.secretStore.Store("http_session", sessionID, []byte(masterSecret))
		if err != nil {
			return rec, fmt.Errorf("%w: %w", ErrPersistenceFailed, err)
		}
		rec.SecretRef = ref
		storedNewSecret = true
	}

	if err := s.saveLocked(); err != nil {
		if storedNewSecret && s.secretStore != nil {
			_ = s.secretStore.Delete("http_session", sessionID, rec.SecretRef)
			rec.SecretRef = ""
		}
		return rec, err
	}
	return rec, nil
}

// AuthenticateBearer validates a bearer token and verifies session_id and device_id binding.
// Both claimedSessionID and claimedDeviceID must be non-empty and match the token's session.
func (s *SessionStore) AuthenticateBearer(token, claimedSessionID, claimedDeviceID string) (*SessionRecord, error) {
	if token == "" {
		return nil, ErrSessionNotFound
	}
	if claimedSessionID == "" || claimedDeviceID == "" {
		return nil, ErrMissingBinding
	}

	tokenHash := HashSessionToken(token)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched *SessionRecord
	for _, rec := range s.sessions {
		if subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(tokenHash)) == 1 {
			matched = rec
			break
		}
	}
	if matched == nil {
		if _, revSess := s.revokedSessions[claimedSessionID]; revSess {
			return nil, ErrSessionRevoked
		}
		if _, revDev := s.revokedDevices[claimedDeviceID]; revDev {
			return nil, ErrSessionRevoked
		}
		return nil, ErrSessionNotFound
	}
	if matched.SessionID != claimedSessionID || matched.DeviceID != claimedDeviceID {
		return nil, ErrSessionBindingMismatch
	}
	_, revSess := s.revokedSessions[matched.SessionID]
	_, revDev := s.revokedDevices[matched.DeviceID]
	if matched.Revoked || revSess || revDev {
		return nil, ErrSessionRevoked
	}
	if matched.IsExpired() {
		return nil, ErrSessionExpired
	}
	return matched, nil
}

// VerifySessionToken verifies a candidate raw session token against the stored SHA-256 hash
// in constant time without persisting or exposing raw bearer tokens.
func (s *SessionStore) VerifySessionToken(rec *SessionRecord, rawToken string) error {
	if rec == nil || rawToken == "" {
		return ErrSessionNotFound
	}
	tokenHash := HashSessionToken(rawToken)
	if subtle.ConstantTimeCompare([]byte(rec.TokenHash), []byte(tokenHash)) != 1 {
		return ErrSessionNotFound
	}
	if rec.Revoked {
		return ErrSessionRevoked
	}
	if rec.IsExpired() {
		return ErrSessionExpired
	}
	return nil
}

// SetLifecycleHooks registers optional callbacks invoked when a session becomes active or is revoked.
func (s *SessionStore) SetLifecycleHooks(onUsed func(sessionID string), onRevoked func(sessionID, deviceID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSessionUsed = onUsed
	s.onSessionRevoked = onRevoked
}

// MarkSessionUsed notifies registered lifecycle hooks that the client has actively authenticated with sessionID.
func (s *SessionStore) MarkSessionUsed(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.RLock()
	cb := s.onSessionUsed
	s.mu.RUnlock()
	if cb != nil {
		cb(sessionID)
	}
}

// GetActiveSession returns an active, non-expired, non-revoked session bound to sessionID and deviceID.
func (s *SessionStore) GetActiveSession(sessionID, deviceID string) (*SessionRecord, error) {
	if sessionID == "" || deviceID == "" {
		return nil, ErrMissingBinding
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, revSess := s.revokedSessions[sessionID]; revSess {
		return nil, ErrSessionRevoked
	}
	if _, revDev := s.revokedDevices[deviceID]; revDev {
		return nil, ErrSessionRevoked
	}

	rec, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if rec.DeviceID != deviceID {
		return nil, ErrSessionBindingMismatch
	}
	if rec.Revoked {
		return nil, ErrSessionRevoked
	}
	if rec.IsExpired() {
		return nil, ErrSessionExpired
	}
	return rec, nil
}

// RevokeSession idempotently revokes sessionID in memory (if loaded), destroys any sealed secret
// referenced by memory or by persisted state, and records a durable revocation tombstone even when
// sessionID is currently absent from memory (e.g. after a restart without secret-store availability).
// The returned boolean indicates whether an active (non-revoked) in-memory session transitioned to
// revoked during this call; false means the session was not active in memory (already revoked, or
// never loaded), which is not an error. Any secret-deletion or tombstone-persistence failure is
// returned via err after in-memory revocation has been applied.
func (s *SessionStore) RevokeSession(sessionID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return false, errors.New("session_id is required")
	}
	now := time.Now().UTC()

	s.mu.Lock()
	rec, ok := s.sessions[sessionID]
	wasActive := false
	var oldRef, devID string
	if ok {
		wasActive = !rec.Revoked
		oldRef = rec.SecretRef
		devID = rec.DeviceID
		rec.Revoked = true
		if rec.RevokedAt.IsZero() {
			rec.RevokedAt = now
		}
		rec.destroyKeysLocked()
	}
	if _, alreadyTombstoned := s.revokedSessions[sessionID]; !alreadyTombstoned {
		s.revokedSessions[sessionID] = now
	}
	cb := s.onSessionRevoked
	ss := s.secretStore
	persistSecrets := s.persistSecrets
	hasStorePath := s.storePath != ""

	var errs []error
	if hasStorePath && persistSecrets && ss != nil {
		refToDelete := oldRef
		if refToDelete == "" {
			// The record may be absent from memory (e.g. restart without the OS secret store) or its
			// in-memory ref may already be cleared. Fall back to the persisted reference so the sealed
			// secret is destroyed before the tombstone is written; even if the atomic tombstone write
			// then fails, stale persisted state can no longer unseal the credentials.
			persisted, readErr := s.persistedSecretRefsLocked(func(e PersistedSessionEntry) bool {
				return e.SessionID == sessionID
			})
			if readErr != nil {
				errs = append(errs, readErr)
			} else if len(persisted) > 0 {
				refToDelete = persisted[0].SecretRef
			}
		}
		if refToDelete != "" {
			if err := ss.Delete("http_session", sessionID, refToDelete); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := s.saveLocked(); err != nil {
		errs = append(errs, err)
	}
	s.mu.Unlock()

	if cb != nil && wasActive {
		cb(sessionID, devID)
	}
	if len(errs) > 0 {
		return wasActive, fmt.Errorf("%w: %w", ErrPersistenceFailed, errors.Join(errs...))
	}
	return wasActive, nil
}

// RevokeDevice permanently revokes all sessions belonging to deviceID, destroys the sealed secrets of
// both loaded and persisted-only sessions on that device, and persists the device/session revocation
// tombstones. In-memory revocation always applies; any secret-deletion or persistence failure is
// returned so callers never assume durable revocation.
func (s *SessionStore) RevokeDevice(deviceID string) (int, error) {
	if deviceID == "" {
		return 0, nil
	}
	now := time.Now().UTC()

	s.mu.Lock()
	s.revokedDevices[deviceID] = now
	count := 0
	var revokedIDs []string
	oldRefs := make(map[string]string)

	if sessIDs, ok := s.deviceSessions[deviceID]; ok {
		for sessID := range sessIDs {
			revokedIDs = append(revokedIDs, sessID)
			s.revokedSessions[sessID] = now
			if rec, exists := s.sessions[sessID]; exists {
				if !rec.Revoked {
					count++
				}
				if rec.SecretRef != "" {
					oldRefs[sessID] = rec.SecretRef
				}
				rec.Revoked = true
				rec.RevokedAt = now
				rec.destroyKeysLocked()
			}
		}
	}

	cb := s.onSessionRevoked
	ss := s.secretStore
	persistSecrets := s.persistSecrets
	hasStorePath := s.storePath != ""

	var errs []error
	if hasStorePath && persistSecrets && ss != nil {
		// Also destroy sealed secrets only referenced by persisted state so device revocation does
		// not leave recoverable ciphertext behind for sessions that were not loaded into memory.
		persisted, readErr := s.persistedSecretRefsLocked(func(e PersistedSessionEntry) bool {
			return e.DeviceID == deviceID
		})
		if readErr != nil {
			errs = append(errs, readErr)
		}
		for _, p := range persisted {
			if _, exists := oldRefs[p.SessionID]; !exists {
				oldRefs[p.SessionID] = p.SecretRef
			}
		}
		for sessID, ref := range oldRefs {
			if err := ss.Delete("http_session", sessID, ref); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := s.saveLocked(); err != nil {
		errs = append(errs, err)
	}
	s.mu.Unlock()

	if cb != nil {
		for _, sid := range revokedIDs {
			cb(sid, deviceID)
		}
	}
	if len(errs) > 0 {
		return count, fmt.Errorf("%w: %w", ErrPersistenceFailed, errors.Join(errs...))
	}
	return count, nil
}

// RemoveSession removes a partially or freshly provisioned session during pairing rollback,
// destroys its in-memory keys, deletes any stored secret, and records a durable revocation
// tombstone so stale persisted state can never resurrect the rolled-back session ID.
func (s *SessionStore) RemoveSession(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("session_id is required")
	}
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, alreadyTombstoned := s.revokedSessions[sessionID]; !alreadyTombstoned {
		s.revokedSessions[sessionID] = now
	}

	var oldRef string
	if rec, ok := s.sessions[sessionID]; ok {
		oldRef = rec.SecretRef
		rec.Revoked = true
		rec.RevokedAt = now
		rec.destroyKeysLocked()
		if m, exists := s.deviceSessions[rec.DeviceID]; exists {
			delete(m, sessionID)
			if len(m) == 0 {
				delete(s.deviceSessions, rec.DeviceID)
			}
		}
		delete(s.sessions, sessionID)
	}

	var errs []error
	if s.storePath != "" && s.persistSecrets && s.secretStore != nil {
		refToDelete := oldRef
		if refToDelete == "" {
			persisted, readErr := s.persistedSecretRefsLocked(func(e PersistedSessionEntry) bool {
				return e.SessionID == sessionID
			})
			if readErr != nil {
				errs = append(errs, readErr)
			} else if len(persisted) > 0 {
				refToDelete = persisted[0].SecretRef
			}
		}
		if refToDelete != "" {
			if err := s.secretStore.Delete("http_session", sessionID, refToDelete); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := s.saveLocked(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrPersistenceFailed, errors.Join(errs...))
	}
	return nil
}

// ParseTimestamp converts integer timestamps (seconds, milliseconds, microseconds, or nanoseconds)
// or RFC3339 strings into UTC time.Time.
func ParseTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return ParseUnixTimestampInt64(n)
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// ParseUnixTimestampInt64 converts a Unix timestamp in seconds, ms, us, or ns to UTC time.Time.
func ParseUnixTimestampInt64(ts int64) (time.Time, bool) {
	switch {
	case ts >= 1e17:
		return time.Unix(0, ts).UTC(), true
	case ts >= 1e14:
		return time.UnixMicro(ts).UTC(), true
	case ts >= 1e11:
		return time.UnixMilli(ts).UTC(), true
	case ts >= 1e8:
		return time.Unix(ts, 0).UTC(), true
	default:
		return time.Time{}, false
	}
}

// IsTimestampFresh verifies that ts is within acceptable clock skew of now.
func IsTimestampFresh(ts, now time.Time) bool {
	if ts.IsZero() {
		return false
	}
	age := now.UTC().Sub(ts.UTC())
	if age > MaxClockSkewPast {
		return false
	}
	if age < -MaxClockSkewFuture {
		return false
	}
	return true
}
