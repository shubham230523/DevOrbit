package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/hkdf"
)

var (
	ErrReplayedFrame     = errors.New("security error: replayed or out-of-sequence frame rejected")
	ErrTamperedPayload   = errors.New("decryption failed: invalid key, AAD mismatch, or tampered ciphertext")
	ErrExpiredSessionKey = errors.New("session key expired or destroyed")
)

type Direction string

const (
	DirClientToHost Direction = "client_to_host"
	DirHostToClient Direction = "host_to_client"

	ChannelBrokerWS   = ""
	ChannelDirectHTTP = "http"
)

type E2ESession struct {
	SessionID       string
	DeviceID        string
	ClientToHostKey []byte
	HostToClientKey []byte
	CreatedAt       time.Time
	ExpiresAt       time.Time
	mu              sync.Mutex
	lastSeqNum      int64
	seenSeqNums     map[int64]bool
}

func DeriveDirectionalKeys(masterSecret string, sessionID, deviceID string) ([]byte, []byte, error) {
	return DeriveChannelDirectionalKeys(ChannelBrokerWS, masterSecret, sessionID, deviceID)
}

// DeriveChannelDirectionalKeys derives independent AES-256 keys per transport channel
// using HKDF-SHA256 domain separation so frames from one channel cannot be replayed on another.
func DeriveChannelDirectionalKeys(channel, masterSecret, sessionID, deviceID string) ([]byte, []byte, error) {
	secretBytes := []byte(masterSecret)
	salt := []byte(fmt.Sprintf("devorbit-e2e-salt-%s", sessionID))
	prefix := "devorbit"
	if channel != "" {
		prefix = "devorbit-" + channel
	}
	infoClient := []byte(fmt.Sprintf("%s-client-to-host-%s-%s", prefix, sessionID, deviceID))
	infoHost := []byte(fmt.Sprintf("%s-host-to-client-%s-%s", prefix, sessionID, deviceID))

	hkdfClient := hkdf.New(sha256.New, secretBytes, salt, infoClient)
	clientKey := make([]byte, 32) // AES-256
	if _, err := io.ReadFull(hkdfClient, clientKey); err != nil {
		return nil, nil, err
	}

	hkdfHost := hkdf.New(sha256.New, secretBytes, salt, infoHost)
	hostKey := make([]byte, 32) // AES-256
	if _, err := io.ReadFull(hkdfHost, hostKey); err != nil {
		return nil, nil, err
	}

	return clientKey, hostKey, nil
}

// DeriveHTTPRequestAuthKey derives a 32-byte HMAC key from the session master secret
// bound to sessionID and deviceID for authenticating direct HTTPS requests.
func DeriveHTTPRequestAuthKey(masterSecret, sessionID, deviceID string) ([]byte, error) {
	salt := []byte(fmt.Sprintf("devorbit-e2e-salt-%s", sessionID))
	info := []byte(fmt.Sprintf("devorbit-http-auth-%s-%s", sessionID, deviceID))
	r := hkdf.New(sha256.New, []byte(masterSecret), salt, info)
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// ComputeHTTPRequestMAC computes the canonical HMAC-SHA256 signature for a direct HTTPS request.
func ComputeHTTPRequestMAC(authKey []byte, method, requestURI, sessionID, deviceID, timestamp, nonce, bodySHA256Hex string) string {
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n%s",
		method, requestURI, sessionID, deviceID, timestamp, nonce, bodySHA256Hex)
	mac := hmac.New(sha256.New, authKey)
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func NewE2ESession(sessionID, deviceID, masterSecret string, ttl time.Duration) (*E2ESession, error) {
	return NewChannelE2ESession(ChannelBrokerWS, sessionID, deviceID, masterSecret, ttl)
}

// NewChannelE2ESession constructs an E2ESession with channel-specific HKDF key separation.
func NewChannelE2ESession(channel, sessionID, deviceID, masterSecret string, ttl time.Duration) (*E2ESession, error) {
	cKey, hKey, err := DeriveChannelDirectionalKeys(channel, masterSecret, sessionID, deviceID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	return &E2ESession{
		SessionID:       sessionID,
		DeviceID:        deviceID,
		ClientToHostKey: cKey,
		HostToClientKey: hKey,
		CreatedAt:       now,
		ExpiresAt:       now.Add(ttl),
		seenSeqNums:     make(map[int64]bool),
	}, nil
}

func (s *E2ESession) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

func (s *E2ESession) Destroy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.ClientToHostKey {
		s.ClientToHostKey[i] = 0
	}
	for i := range s.HostToClientKey {
		s.HostToClientKey[i] = 0
	}
	s.seenSeqNums = make(map[int64]bool)
}

func (s *E2ESession) ConstructAAD(seqNum int64, timestamp int64) []byte {
	aad := make([]byte, 0, 64)
	aad = append(aad, []byte(s.SessionID)...)
	aad = append(aad, ':')
	aad = append(aad, []byte(s.DeviceID)...)
	aad = append(aad, ':')

	seqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBytes, uint64(seqNum))
	aad = append(aad, seqBytes...)

	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(timestamp))
	aad = append(aad, tsBytes...)

	return aad
}

func (s *E2ESession) Encrypt(dir Direction, plainText []byte, seqNum int64, timestamp int64) (string, error) {
	if s.IsExpired() {
		return "", ErrExpiredSessionKey
	}

	s.mu.Lock()
	if isZeroKey(s.ClientToHostKey) || isZeroKey(s.HostToClientKey) {
		s.mu.Unlock()
		return "", ErrExpiredSessionKey
	}

	var key []byte
	if dir == DirHostToClient {
		key = append([]byte(nil), s.HostToClientKey...)
	} else {
		key = append([]byte(nil), s.ClientToHostKey...)
	}
	s.mu.Unlock()

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	aad := s.ConstructAAD(seqNum, timestamp)
	cipherText := aesGCM.Seal(nonce, nonce, plainText, aad)
	return hex.EncodeToString(cipherText), nil
}

const ReplayWindowSize int64 = 1000

func isZeroKey(k []byte) bool {
	if len(k) == 0 {
		return true
	}
	for _, b := range k {
		if b != 0 {
			return false
		}
	}
	return true
}

func (s *E2ESession) Decrypt(dir Direction, cipherTextHex string, seqNum int64, timestamp int64) ([]byte, error) {
	if s.IsExpired() {
		return nil, ErrExpiredSessionKey
	}

	s.mu.Lock()
	if isZeroKey(s.ClientToHostKey) || isZeroKey(s.HostToClientKey) {
		s.mu.Unlock()
		return nil, ErrExpiredSessionKey
	}

	// 1. Check bounds and sliding window before decryption
	if seqNum <= 0 {
		s.mu.Unlock()
		return nil, ErrReplayedFrame
	}

	if s.lastSeqNum > ReplayWindowSize && seqNum <= (s.lastSeqNum-ReplayWindowSize) {
		s.mu.Unlock()
		return nil, ErrReplayedFrame
	}

	if s.seenSeqNums[seqNum] {
		s.mu.Unlock()
		return nil, ErrReplayedFrame
	}

	var key []byte
	if dir == DirClientToHost {
		key = append([]byte(nil), s.ClientToHostKey...)
	} else {
		key = append([]byte(nil), s.HostToClientKey...)
	}
	s.mu.Unlock()

	// 2. Authenticate & Decrypt (AES-GCM tag verification) BEFORE committing replay state
	cipherText, err := hex.DecodeString(cipherTextHex)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	if len(cipherText) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, actualCipherText := cipherText[:nonceSize], cipherText[nonceSize:]
	aad := s.ConstructAAD(seqNum, timestamp)

	plainText, err := aesGCM.Open(nil, nonce, actualCipherText, aad)
	if err != nil {
		// Failed authentication! Replay state remains completely untouched.
		return nil, ErrTamperedPayload
	}

	// 3. Atomically commit sequence number to sliding replay window AFTER successful authentication
	s.mu.Lock()
	defer s.mu.Unlock()

	if isZeroKey(s.ClientToHostKey) || isZeroKey(s.HostToClientKey) {
		return nil, ErrExpiredSessionKey
	}

	if s.seenSeqNums[seqNum] {
		return nil, ErrReplayedFrame
	}

	if s.lastSeqNum > ReplayWindowSize && seqNum <= (s.lastSeqNum-ReplayWindowSize) {
		return nil, ErrReplayedFrame
	}

	s.seenSeqNums[seqNum] = true
	if seqNum > s.lastSeqNum {
		s.lastSeqNum = seqNum
	}

	// Prune sequence numbers older than sliding window bound
	if s.lastSeqNum > ReplayWindowSize {
		cutoff := s.lastSeqNum - ReplayWindowSize
		for k := range s.seenSeqNums {
			if k <= cutoff {
				delete(s.seenSeqNums, k)
			}
		}
	}

	return plainText, nil
}
