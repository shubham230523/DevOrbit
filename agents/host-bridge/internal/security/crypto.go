package security

import (
	"crypto/aes"
	"crypto/cipher"
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
	secretBytes := []byte(masterSecret)
	salt := []byte(fmt.Sprintf("devorbit-e2e-salt-%s", sessionID))
	infoClient := []byte(fmt.Sprintf("devorbit-client-to-host-%s-%s", sessionID, deviceID))
	infoHost := []byte(fmt.Sprintf("devorbit-host-to-client-%s-%s", sessionID, deviceID))

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

func NewE2ESession(sessionID, deviceID, masterSecret string, ttl time.Duration) (*E2ESession, error) {
	cKey, hKey, err := DeriveDirectionalKeys(masterSecret, sessionID, deviceID)
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

	var key []byte
	if dir == DirHostToClient {
		key = s.HostToClientKey
	} else {
		key = s.ClientToHostKey
	}

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

func (s *E2ESession) Decrypt(dir Direction, cipherTextHex string, seqNum int64, timestamp int64) ([]byte, error) {
	if s.IsExpired() {
		return nil, ErrExpiredSessionKey
	}

	s.mu.Lock()
	if s.seenSeqNums[seqNum] || seqNum <= 0 {
		s.mu.Unlock()
		return nil, ErrReplayedFrame
	}
	s.seenSeqNums[seqNum] = true
	s.lastSeqNum = seqNum
	s.mu.Unlock()

	rollbackSeq := func() {
		s.mu.Lock()
		delete(s.seenSeqNums, seqNum)
		s.mu.Unlock()
	}

	cipherText, err := hex.DecodeString(cipherTextHex)
	if err != nil {
		rollbackSeq()
		return nil, err
	}

	var key []byte
	if dir == DirClientToHost {
		key = s.ClientToHostKey
	} else {
		key = s.HostToClientKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		rollbackSeq()
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		rollbackSeq()
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	if len(cipherText) < nonceSize {
		rollbackSeq()
		return nil, errors.New("ciphertext too short")
	}

	nonce, actualCipherText := cipherText[:nonceSize], cipherText[nonceSize:]
	aad := s.ConstructAAD(seqNum, timestamp)

	plainText, err := aesGCM.Open(nil, nonce, actualCipherText, aad)
	if err != nil {
		rollbackSeq()
		return nil, ErrTamperedPayload
	}

	s.mu.Lock()
	if len(s.seenSeqNums) > 2000 {
		cutoff := s.lastSeqNum - 1000
		for k := range s.seenSeqNums {
			if k < cutoff {
				delete(s.seenSeqNums, k)
			}
		}
	}
	s.mu.Unlock()

	return plainText, nil
}
