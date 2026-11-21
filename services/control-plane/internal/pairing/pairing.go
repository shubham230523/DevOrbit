package pairing

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
	ID        string    `json:"id"`
	MachineID string    `json:"machine_id"`
	HostName  string    `json:"host_name"`
	Payload   string    `json:"payload"`
	Consumed  bool      `json:"consumed"`
	ExpiresAt time.Time `json:"expires_at"`
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

func (s *Service) CreateChallenge(machineID, hostName string, ttl time.Duration) (*Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	chID := fmt.Sprintf("ch-%s", hex.EncodeToString(b))

	payloadBytes := make([]byte, 32)
	_, _ = rand.Read(payloadBytes)
	payloadStr := hex.EncodeToString(payloadBytes)

	ch := &Challenge{
		ID:        chID,
		MachineID: machineID,
		HostName:  hostName,
		Payload:   payloadStr,
		Consumed:  false,
		ExpiresAt: time.Now().Add(ttl),
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

	// Verify cryptographic proof of possession: HMAC-SHA256(secretKey, ch.Payload + deviceID)
	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write([]byte(ch.Payload + ":" + deviceID))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(proofSig), []byte(expectedSig)) {
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

func (s *Service) GenerateProof(chPayload, deviceID string) string {
	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write([]byte(chPayload + ":" + deviceID))
	return hex.EncodeToString(mac.Sum(nil))
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
