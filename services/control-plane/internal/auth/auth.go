package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	secretKey []byte
}

func NewService(secret string) *Service {
	return &Service{secretKey: []byte(secret)}
}

func (s *Service) GenerateToken(machineID, deviceID string) (string, error) {
	if machineID == "" || deviceID == "" {
		return "", errors.New("machineID and deviceID cannot be empty")
	}
	timestamp := time.Now().Unix()
	payload := fmt.Sprintf("%s:%s:%d", machineID, deviceID, timestamp)

	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s.%s", payload, signature), nil
}

func (s *Service) ValidateToken(token string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", "", errors.New("invalid token format")
	}
	payload, signature := parts[0], parts[1]

	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return "", "", errors.New("invalid token signature")
	}

	fields := strings.Split(payload, ":")
	if len(fields) != 3 {
		return "", "", errors.New("malformed payload")
	}

	return fields[0], fields[1], nil
}
