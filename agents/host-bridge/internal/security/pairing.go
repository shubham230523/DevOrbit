package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrPairingExpired = errors.New("pairing challenge has expired")
	ErrPairingUsed    = errors.New("pairing challenge has already been consumed")
	ErrInvalidProof   = errors.New("invalid cryptographic proof of possession")
)

type PairingChallenge struct {
	Version      string    `json:"v"`
	ID           string    `json:"challenge_id"`
	HostID       string    `json:"host_id"`
	HostName     string    `json:"host_name"`
	Payload      string    `json:"pairing_payload"`
	Secret       string    `json:"pairing_secret,omitempty"`
	Consumed     bool      `json:"consumed"`
	ExpiresAt    time.Time `json:"expires_at"`
	ExpiresAtStr string    `json:"expires_at_str"`

	// Retry cache for transient network failure before the paired session is activated or revoked.
	// Note: pairing retries always require a fresh timestamp, unseen nonce, and v1.1 HMAC proof,
	// and cached credentials are wiped as soon as the session is used or revoked.
	CachedDeviceID     string               `json:"-"`
	CachedSessionToken string               `json:"-"`
	CachedSessionID    string               `json:"-"`
	Invalidated        bool                 `json:"-"`
	seenNonces         map[string]time.Time `json:"-"`
}

type PairingEngine struct {
	mu                 sync.RWMutex
	challenges         map[string]*PairingChallenge
	sessionToChallenge map[string]string
	secretKey          []byte
	hostID             string
	hostName           string
}

func NewPairingEngine(hostID, hostName, secret string) *PairingEngine {
	return &PairingEngine{
		challenges:         make(map[string]*PairingChallenge),
		sessionToChallenge: make(map[string]string),
		secretKey:          []byte(secret),
		hostID:             hostID,
		hostName:           hostName,
	}
}

func (p *PairingEngine) HostID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hostID
}

func (p *PairingEngine) HostName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hostName
}

func ConstructProofAuthenticatedData(version, challengeID, payload, hostID, deviceID, expiresAtStr string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", version, challengeID, payload, hostID, deviceID, expiresAtStr)
}

// ConstructProofAuthenticatedDataV11 binds version, challenge, payload, host, device, expiry,
// plus per-request timestamp and nonce to prevent captured request replay.
func ConstructProofAuthenticatedDataV11(version, challengeID, payload, hostID, deviceID, expiresAtStr, timestamp, nonce string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s:%s", version, challengeID, payload, hostID, deviceID, expiresAtStr, timestamp, nonce)
}

func GenerateProof(version, challengeID, payload, hostID, deviceID, expiresAtStr, ephemeralSecret string) string {
	authData := ConstructProofAuthenticatedData(version, challengeID, payload, hostID, deviceID, expiresAtStr)
	mac := hmac.New(sha256.New, []byte(ephemeralSecret))
	mac.Write([]byte(authData))
	return hex.EncodeToString(mac.Sum(nil))
}

func GenerateProofV11(challengeID, payload, hostID, deviceID, expiresAtStr, timestamp, nonce, ephemeralSecret string) string {
	authData := ConstructProofAuthenticatedDataV11("1.1", challengeID, payload, hostID, deviceID, expiresAtStr, timestamp, nonce)
	mac := hmac.New(sha256.New, []byte(ephemeralSecret))
	mac.Write([]byte(authData))
	return hex.EncodeToString(mac.Sum(nil))
}

func (p *PairingEngine) CreateChallenge(ttl time.Duration) (*PairingChallenge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate challenge ID: %w", err)
	}
	chID := fmt.Sprintf("qr-%s", hex.EncodeToString(b))

	payloadBytes := make([]byte, 32)
	if _, err := rand.Read(payloadBytes); err != nil {
		return nil, fmt.Errorf("failed to generate pairing payload: %w", err)
	}
	payloadStr := hex.EncodeToString(payloadBytes)

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral pairing secret: %w", err)
	}
	secretStr := hex.EncodeToString(secretBytes)

	expiresAt := time.Now().Add(ttl)
	// Always store as UTC RFC3339 so the HMAC authData string is timezone-independent.
	expiresAtStr := expiresAt.UTC().Format(time.RFC3339)

	ch := &PairingChallenge{
		Version:      "1.0",
		ID:           chID,
		HostID:       p.hostID,
		HostName:     p.hostName,
		Payload:      payloadStr,
		Secret:       secretStr,
		Consumed:     false,
		ExpiresAt:    expiresAt,
		ExpiresAtStr: expiresAtStr,
		seenNonces:   make(map[string]time.Time),
	}

	p.challenges[chID] = ch
	return ch, nil
}

// ConfirmPairing verifies challenge liveness, per-attempt timestamp/nonce freshness (anti-replay),
// and constant-time HMAC proof of possession using the challenge's ephemeral secret.
// Returns (sessionToken, ephemeralPairingSecret, challengeExpiresAt, err).
func (p *PairingEngine) ConfirmPairing(chID, deviceID, deviceName, timestampRaw, nonce, proofSig string) (string, string, time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, found := p.challenges[chID]
	if !found {
		return "", "", time.Time{}, errors.New("pairing challenge not found")
	}

	now := time.Now().UTC()
	if now.After(ch.ExpiresAt.UTC()) {
		return "", "", time.Time{}, ErrPairingExpired
	}

	if ch.Invalidated {
		return "", "", time.Time{}, ErrPairingUsed
	}

	if ch.Consumed && (ch.CachedDeviceID != deviceID || ch.CachedSessionToken == "") {
		return "", "", time.Time{}, ErrPairingUsed
	}

	if ch.Secret == "" {
		return "", "", time.Time{}, errors.New("challenge has no ephemeral pairing secret")
	}

	if len(nonce) < 16 || len(nonce) > 128 {
		return "", "", time.Time{}, ErrReplayDetected
	}
	tsTime, tsValid := ParseTimestamp(timestampRaw)
	if !tsValid || !IsTimestampFresh(tsTime, now) {
		return "", "", time.Time{}, ErrInvalidTimestamp
	}

	if ch.seenNonces == nil {
		ch.seenNonces = make(map[string]time.Time)
	}
	for k, exp := range ch.seenNonces {
		if now.After(exp) {
			delete(ch.seenNonces, k)
		}
	}
	if _, seen := ch.seenNonces[nonce]; seen {
		return "", "", time.Time{}, ErrReplayDetected
	}

	// Verify cryptographic proof of possession EXCLUSIVELY with the challenge's ephemeral secret
	// and fresh per-attempt timestamp + nonce.
	expectedAuthData := ConstructProofAuthenticatedDataV11("1.1", ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr, timestampRaw, nonce)
	mac := hmac.New(sha256.New, []byte(ch.Secret))
	mac.Write([]byte(expectedAuthData))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(proofSig), []byte(expectedSig)) != 1 {
		return "", "", time.Time{}, ErrInvalidProof
	}

	// Mark nonce consumed ONLY after HMAC proof succeeds to prevent unauthenticated cache pollution
	// while ensuring any captured valid request cannot be replayed.
	ch.seenNonces[nonce] = now.Add(NonceRetentionTTL)

	if ch.Consumed {
		return ch.CachedSessionToken, ch.Secret, ch.ExpiresAt, nil
	}

	ch.Consumed = true

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to generate session token: %w", err)
	}
	sessionToken := hex.EncodeToString(tokenBytes)

	ch.CachedDeviceID = deviceID
	ch.CachedSessionToken = sessionToken

	return sessionToken, ch.Secret, ch.ExpiresAt, nil
}

// BindChallengeSession links a provisioned session ID to its originating pairing challenge
// so activation or revocation can invalidate future pairing retry recovery.
func (p *PairingEngine) BindChallengeSession(chID, sessionID string) {
	if chID == "" || sessionID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if ch, found := p.challenges[chID]; found {
		ch.CachedSessionID = sessionID
		if p.sessionToChallenge == nil {
			p.sessionToChallenge = make(map[string]string)
		}
		p.sessionToChallenge[sessionID] = chID
	}
}

// InvalidateSessionRetry closes the pairing confirmation retry window once a session has
// been actively used or revoked, preventing later credential recovery.
func (p *PairingEngine) InvalidateSessionRetry(sessionID string) {
	if sessionID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	chID, ok := p.sessionToChallenge[sessionID]
	if !ok {
		return
	}
	if ch, found := p.challenges[chID]; found {
		ch.Invalidated = true
		ch.CachedSessionToken = ""
		ch.Secret = ""
	}
}

func (p *PairingEngine) UnconsumeChallenge(chID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ch, found := p.challenges[chID]; found {
		if ch.CachedSessionID != "" {
			delete(p.sessionToChallenge, ch.CachedSessionID)
		}
		ch.Consumed = false
		ch.Invalidated = false
		ch.CachedDeviceID = ""
		ch.CachedSessionToken = ""
		ch.CachedSessionID = ""
	}
}

func (p *PairingEngine) GenerateProofForChallenge(chID, deviceID, timestamp, nonce string) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ch, found := p.challenges[chID]
	if !found {
		return "", errors.New("challenge not found")
	}
	return GenerateProofV11(ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr, timestamp, nonce, ch.Secret), nil
}
