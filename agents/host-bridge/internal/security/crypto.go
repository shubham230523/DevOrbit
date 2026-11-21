package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

type E2ECipher struct {
	key []byte
}

func NewE2ECipher(secret string) *E2ECipher {
	hash := sha256.Sum256([]byte(secret))
	return &E2ECipher{key: hash[:]}
}

func (c *E2ECipher) EncryptPayload(plainText []byte) (string, error) {
	block, err := aes.NewCipher(c.key)
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

	cipherText := aesGCM.Seal(nonce, nonce, plainText, nil)
	return hex.EncodeToString(cipherText), nil
}

func (c *E2ECipher) DecryptPayload(cipherTextHex string) ([]byte, error) {
	cipherText, err := hex.DecodeString(cipherTextHex)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(c.key)
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
	plainText, err := aesGCM.Open(nil, nonce, actualCipherText, nil)
	if err != nil {
		return nil, errors.New("decryption failed: invalid key or tampered ciphertext")
	}

	return plainText, nil
}
