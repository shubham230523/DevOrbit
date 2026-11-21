package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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
	ID        string    `json:"challenge_id"`
	HostID    string    `json:"host_id"`
	HostName  string    `json:"host_name"`
	Payload   string    `json:"pairing_payload"`
	Consumed  bool      `json:"consumed"`
	ExpiresAt time.Time `json:"expires_at"`
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

func (p *PairingEngine) CreateChallenge(ttl time.Duration) (*PairingChallenge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	chID := fmt.Sprintf("qr-%s", hex.EncodeToString(b))

	payloadBytes := make([]byte, 32)
	_, _ = rand.Read(payloadBytes)
	payloadStr := hex.EncodeToString(payloadBytes)

	ch := &PairingChallenge{
		ID:        chID,
		HostID:    p.hostID,
		HostName:  p.hostName,
		Payload:   payloadStr,
		Consumed:  false,
		ExpiresAt: time.Now().Add(ttl),
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

	mac := hmac.New(sha256.New, p.secretKey)
	mac.Write([]byte(ch.Payload + ":" + deviceID))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(proofSig), []byte(expectedSig)) {
		return "", ErrInvalidProof
	}

	ch.Consumed = true

	// Return short-lived session token
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	sessionToken := hex.EncodeToString(tokenBytes)

	return sessionToken, nil
}

func (p *PairingEngine) GenerateProof(chPayload, deviceID string) string {
	mac := hmac.New(sha256.New, p.secretKey)
	mac.Write([]byte(chPayload + ":" + deviceID))
	return hex.EncodeToString(mac.Sum(nil))
}
