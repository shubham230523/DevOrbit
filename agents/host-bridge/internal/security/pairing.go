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
}

type PairingEngine struct {
	mu         sync.RWMutex
	challenges map[string]*PairingChallenge
	secretKey  []byte
	hostID     string
	hostName   string
}

func NewPairingEngine(hostID, hostName, secret string) *PairingEngine {
	return &PairingEngine{
		challenges: make(map[string]*PairingChallenge),
		secretKey:  []byte(secret),
		hostID:     hostID,
		hostName:   hostName,
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

func GenerateProof(version, challengeID, payload, hostID, deviceID, expiresAtStr, ephemeralSecret string) string {
	authData := ConstructProofAuthenticatedData(version, challengeID, payload, hostID, deviceID, expiresAtStr)
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
	// The QR JSON and the verification path must use the identical string.
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
	}

	p.challenges[chID] = ch
	return ch, nil
}

func (p *PairingEngine) ConfirmPairing(chID, deviceID, deviceName, pubKey, proofSig string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, found := p.challenges[chID]
	if !found {
		return "", errors.New("pairing challenge not found")
	}

	if ch.Consumed {
		return "", ErrPairingUsed
	}

	if time.Now().After(ch.ExpiresAt) {
		return "", ErrPairingExpired
	}

	if ch.Secret == "" {
		return "", errors.New("challenge has no ephemeral pairing secret")
	}

	// Verify cryptographic proof of possession EXCLUSIVELY with the challenge's ephemeral secret
	// Authenticated data binds: protocol version, challenge ID, pairing payload, host ID, device ID, expiry
	expectedAuthData := ConstructProofAuthenticatedData(ch.Version, ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr)
	mac := hmac.New(sha256.New, []byte(ch.Secret))
	mac.Write([]byte(expectedAuthData))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison
	if subtle.ConstantTimeCompare([]byte(proofSig), []byte(expectedSig)) != 1 {
		return "", ErrInvalidProof
	}

	// Mark consumed atomically under lock
	ch.Consumed = true

	// Issue short-lived session client token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}
	sessionToken := hex.EncodeToString(tokenBytes)

	return sessionToken, nil
}

func (p *PairingEngine) GenerateProofForChallenge(chID, deviceID string) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ch, found := p.challenges[chID]
	if !found {
		return "", errors.New("challenge not found")
	}
	return GenerateProof(ch.Version, ch.ID, ch.Payload, ch.HostID, deviceID, ch.ExpiresAtStr, ch.Secret), nil
}
