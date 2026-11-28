package security

import (
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
)

const (
	MaxClockSkewPast   = 5 * time.Minute
	MaxClockSkewFuture = 1 * time.Minute
	NonceRetentionTTL  = 10 * time.Minute
)

// SessionRecord holds the server-authoritative identity, credentials, permissions,
// and cryptographic state for a paired mobile session.
type SessionRecord struct {
	SessionID     string
	DeviceID      string
	TokenHash     string
	MasterSecret  string
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
// Raw bearer tokens are never persisted; only the SHA-256 hash is stored.
type PersistedSessionEntry struct {
	SessionID    string                    `json:"session_id"`
	DeviceID     string                    `json:"device_id"`
	TokenHash    string                    `json:"token_hash"`
	MasterSecret string                    `json:"session_secret"`
	ExpiresAt    time.Time                 `json:"expires_at"`
	Permissions  map[ActionPermission]bool `json:"permissions,omitempty"`
}

// SessionStore manages active, expired, and revoked sessions across direct HTTP
// and Broker WebSocket transports.
type SessionStore struct {
	mu               sync.RWMutex
	sessions         map[string]*SessionRecord
	deviceSessions   map[string]map[string]bool
	revokedDevices   map[string]bool
	storePath        string
	onSessionUsed    func(sessionID string)
	onSessionRevoked func(sessionID, deviceID string)
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions:       make(map[string]*SessionRecord),
		deviceSessions: make(map[string]map[string]bool),
		revokedDevices: make(map[string]bool),
	}
}

func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *SessionStore) EnablePersistence(customPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if customPath != "" {
		s.storePath = customPath
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".devorbit")
	_ = os.MkdirAll(dir, 0700)
	s.storePath = filepath.Join(dir, "host_http_sessions.json")
}

func (s *SessionStore) saveLocked() {
	if s.storePath == "" {
		return
	}
	now := time.Now().UTC()
	var entries []PersistedSessionEntry
	for _, rec := range s.sessions {
		if rec.Revoked || now.After(rec.ExpiresAt) || rec.MasterSecret == "" || rec.TokenHash == "" {
			continue
		}
		var perms map[ActionPermission]bool
		if rec.Permissions != nil {
			perms = rec.Permissions.Granted
		}
		entries = append(entries, PersistedSessionEntry{
			SessionID:    rec.SessionID,
			DeviceID:     rec.DeviceID,
			TokenHash:    rec.TokenHash,
			MasterSecret: rec.MasterSecret,
			ExpiresAt:    rec.ExpiresAt,
			Permissions:  perms,
		})
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.storePath, data, 0600)
	}
}

func (s *SessionStore) LoadPersistedSessions() {
	s.mu.RLock()
	p := s.storePath
	s.mu.RUnlock()
	if p == "" {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var entries []PersistedSessionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	now := time.Now().UTC()
	for _, e := range entries {
		if e.SessionID == "" || e.DeviceID == "" || e.TokenHash == "" || e.MasterSecret == "" {
			continue
		}
		if now.After(e.ExpiresAt) {
			continue
		}
		perms := NewFullSessionPermissions()
		if len(e.Permissions) > 0 {
			perms = &SessionPermissions{Granted: e.Permissions}
		}
		_, _ = s.registerSessionInternal(e.SessionID, e.DeviceID, e.TokenHash, e.MasterSecret, e.ExpiresAt, perms)
	}
}

// RegisterSession provisions a new authenticated session with full validation and E2E key derivation.
func (s *SessionStore) RegisterSession(sessionID, deviceID, sessionToken, masterSecret string, expiresAt time.Time, perms *SessionPermissions) (*SessionRecord, error) {
	if sessionID == "" || deviceID == "" || sessionToken == "" || masterSecret == "" {
		return nil, errors.New("session_id, device_id, session_token, and master_secret are required")
	}
	tokenHash := HashSessionToken(sessionToken)
	return s.registerSessionInternal(sessionID, deviceID, tokenHash, masterSecret, expiresAt, perms)
}

func (s *SessionStore) registerSessionInternal(sessionID, deviceID, tokenHash, masterSecret string, expiresAt time.Time, perms *SessionPermissions) (*SessionRecord, error) {
	expUTC := expiresAt.UTC()
	ttl := time.Until(expUTC)
	if ttl <= 0 {
		return nil, ErrSessionExpired
	}
	if perms == nil {
		perms = NewFullSessionPermissions()
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

	// Re-pairing a previously revoked device issues a new active session and clears device revocation.
	delete(s.revokedDevices, deviceID)

	rec := &SessionRecord{
		SessionID:     sessionID,
		DeviceID:      deviceID,
		TokenHash:     tokenHash,
		MasterSecret:  masterSecret,
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
	s.saveLocked()
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
		return nil, ErrSessionNotFound
	}
	if matched.SessionID != claimedSessionID || matched.DeviceID != claimedDeviceID {
		return nil, ErrSessionBindingMismatch
	}
	if matched.Revoked || s.revokedDevices[matched.DeviceID] {
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

	rec, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if rec.DeviceID != deviceID {
		return nil, ErrSessionBindingMismatch
	}
	if rec.Revoked || s.revokedDevices[rec.DeviceID] {
		return nil, ErrSessionRevoked
	}
	if rec.IsExpired() {
		return nil, ErrSessionExpired
	}
	return rec, nil
}

// RevokeSession permanently revokes a session, destroys its keys, and updates persistence.
func (s *SessionStore) RevokeSession(sessionID string) bool {
	s.mu.Lock()
	rec, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return false
	}
	rec.Revoked = true
	rec.RevokedAt = time.Now().UTC()
	rec.destroyKeysLocked()
	s.saveLocked()
	cb := s.onSessionRevoked
	devID := rec.DeviceID
	s.mu.Unlock()

	if cb != nil {
		cb(sessionID, devID)
	}
	return true
}

// RevokeDevice permanently revokes all sessions belonging to deviceID and updates persistence.
func (s *SessionStore) RevokeDevice(deviceID string) int {
	if deviceID == "" {
		return 0
	}
	s.mu.Lock()
	s.revokedDevices[deviceID] = true
	count := 0
	var revokedIDs []string
	if sessIDs, ok := s.deviceSessions[deviceID]; ok {
		for sessID := range sessIDs {
			revokedIDs = append(revokedIDs, sessID)
			if rec, exists := s.sessions[sessID]; exists {
				if !rec.Revoked {
					count++
				}
				rec.Revoked = true
				rec.RevokedAt = time.Now().UTC()
				rec.destroyKeysLocked()
			}
		}
	}
	s.saveLocked()
	cb := s.onSessionRevoked
	s.mu.Unlock()

	if cb != nil {
		for _, sid := range revokedIDs {
			cb(sid, deviceID)
		}
	}
	return count
}

// RemoveSession deletes a session (used for rollback when downstream provisioning fails).
func (s *SessionStore) RemoveSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.sessions[sessionID]; ok {
		rec.destroyKeysLocked()
		if m, exists := s.deviceSessions[rec.DeviceID]; exists {
			delete(m, sessionID)
			if len(m) == 0 {
				delete(s.deviceSessions, rec.DeviceID)
			}
		}
		delete(s.sessions, sessionID)
		s.saveLocked()
	}
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
