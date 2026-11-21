package pairing

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
	ErrChallengeExpired   = errors.New("pairing challenge expired")
	ErrChallengeUsed      = errors.New("pairing challenge already consumed")
	ErrInvalidProof       = errors.New("invalid cryptographic proof of possession")
	ErrDeviceRevoked      = errors.New("device identity is revoked")
	ErrDeviceNotRegistered = errors.New("device identity not registered")
)

type DeviceState string

const (
	DeviceStateActive  DeviceState = "ACTIVE"
	DeviceStateRevoked DeviceState = "REVOKED"
)

type PairedDevice struct {
	DeviceID        string      `json:"device_id"`
	DeviceName      string      `json:"device_name"`
	MachineID       string      `json:"machine_id"`
	DevicePublicKey string      `json:"device_public_key"`
	State           DeviceState `json:"state"`
	RegisteredAt    time.Time   `json:"registered_at"`
}

type Challenge struct {
	Version      string    `json:"v"`
	ID           string    `json:"id"`
	MachineID    string    `json:"machine_id"`
	HostName     string    `json:"host_name"`
	Payload      string    `json:"payload"`
	Secret       string    `json:"pairing_secret,omitempty"`
	Consumed     bool      `json:"consumed"`
	ExpiresAt    time.Time `json:"expires_at"`
	ExpiresAtStr string    `json:"expires_at_str"`
}

type Service struct {
	mu         sync.RWMutex
	challenges map[string]*Challenge
	devices    map[string]*PairedDevice // key: machineID + ":" + deviceID
	secretKey  []byte
}

func NewService(secret string) *Service {
	return &Service{
		challenges: make(map[string]*Challenge),
		devices:    make(map[string]*PairedDevice),
		secretKey:  []byte(secret),
	}
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

func (s *Service) CreateChallenge(machineID, hostName string, ttl time.Duration) (*Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("failed to generate challenge ID: %w", err)
	}
	chID := fmt.Sprintf("ch-%s", hex.EncodeToString(b))

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
	expiresAtStr := expiresAt.Format(time.RFC3339)

	ch := &Challenge{
		Version:      "1.0",
		ID:           chID,
		MachineID:    machineID,
		HostName:     hostName,
		Payload:      payloadStr,
		Secret:       secretStr,
		Consumed:     false,
		ExpiresAt:    expiresAt,
		ExpiresAtStr: expiresAtStr,
	}

	s.challenges[chID] = ch
	return ch, nil
}

func (s *Service) ConfirmPairing(chID, deviceID, deviceName, pubKey, proofSig string) (*PairedDevice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch, found := s.challenges[chID]
	if !found {
		return nil, errors.New("pairing challenge not found")
	}

	if ch.Consumed {
		return nil, ErrChallengeUsed
	}

	if time.Now().After(ch.ExpiresAt) {
		return nil, ErrChallengeExpired
	}

	if ch.Secret == "" {
		return nil, errors.New("challenge has no ephemeral pairing secret")
	}

	// Verify cryptographic proof of possession EXCLUSIVELY with the challenge's ephemeral secret
	expectedAuthData := ConstructProofAuthenticatedData(ch.Version, ch.ID, ch.Payload, ch.MachineID, deviceID, ch.ExpiresAtStr)
	mac := hmac.New(sha256.New, []byte(ch.Secret))
	mac.Write([]byte(expectedAuthData))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(proofSig), []byte(expectedSig)) != 1 {
		return nil, ErrInvalidProof
	}

	ch.Consumed = true

	devKey := ch.MachineID + ":" + deviceID
	dev := &PairedDevice{
		DeviceID:        deviceID,
		DeviceName:      deviceName,
		MachineID:       ch.MachineID,
		DevicePublicKey: pubKey,
		State:           DeviceStateActive,
		RegisteredAt:    time.Now(),
	}

	s.devices[devKey] = dev
	return dev, nil
}

func (s *Service) GenerateProofForChallenge(chID, deviceID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ch, found := s.challenges[chID]
	if !found {
		return "", errors.New("challenge not found")
	}
	return GenerateProof(ch.Version, ch.ID, ch.Payload, ch.MachineID, deviceID, ch.ExpiresAtStr, ch.Secret), nil
}

func (s *Service) RevokeDevice(machineID, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	devKey := machineID + ":" + deviceID
	dev, found := s.devices[devKey]
	if !found {
		return ErrDeviceNotRegistered
	}

	dev.State = DeviceStateRevoked
	return nil
}

func (s *Service) IsDeviceAuthorized(machineID, deviceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	devKey := machineID + ":" + deviceID
	dev, found := s.devices[devKey]
	if !found {
		return false
	}

	return dev.State == DeviceStateActive
}
