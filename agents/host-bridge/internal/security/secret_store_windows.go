//go:build windows

package security

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const dpapiRefPrefix = "dpapi-v1:"

type dpapiSecretStore struct{}

// NewDefaultSecretStore returns a Windows DPAPI-backed SecretStore bound to the
// current Windows user/service logon credentials via CryptProtectData/CryptUnprotectData.
func NewDefaultSecretStore() SecretStore {
	return &dpapiSecretStore{}
}

func (d *dpapiSecretStore) Name() string {
	return "windows-dpapi"
}

func (d *dpapiSecretStore) Available() bool {
	return true
}

func dpapiEntropy(scope, id string) []byte {
	return []byte("devorbit-secret-store-v1:" + scope + ":" + id)
}

func bytesToDataBlob(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{
		Size: uint32(len(b)),
		Data: &b[0],
	}
}

func (d *dpapiSecretStore) Store(scope, id string, secret []byte) (string, error) {
	if scope == "" || id == "" || len(secret) == 0 {
		return "", errors.New("scope, id, and non-empty secret are required")
	}
	inCopy := append([]byte(nil), secret...)
	defer zeroBytes(inCopy)

	entropy := dpapiEntropy(scope, id)
	inBlob := bytesToDataBlob(inCopy)
	entropyBlob := bytesToDataBlob(entropy)
	var outBlob windows.DataBlob

	err := windows.CryptProtectData(
		&inBlob,
		nil,
		&entropyBlob,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&outBlob,
	)
	if err != nil {
		return "", fmt.Errorf("windows DPAPI CryptProtectData failed: %w", err)
	}
	if outBlob.Data != nil {
		defer func() {
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))
		}()
	}

	cipherBytes := unsafe.Slice(outBlob.Data, int(outBlob.Size))
	return dpapiRefPrefix + base64.StdEncoding.EncodeToString(cipherBytes), nil
}

func (d *dpapiSecretStore) Load(scope, id, ref string) ([]byte, error) {
	if scope == "" || id == "" {
		return nil, errors.New("scope and id are required")
	}
	if !strings.HasPrefix(ref, dpapiRefPrefix) {
		return nil, ErrSecretNotFound
	}
	cipherBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ref, dpapiRefPrefix))
	if err != nil || len(cipherBytes) == 0 {
		return nil, ErrSecretNotFound
	}

	entropy := dpapiEntropy(scope, id)
	inBlob := bytesToDataBlob(cipherBytes)
	entropyBlob := bytesToDataBlob(entropy)
	var outBlob windows.DataBlob

	if err := windows.CryptUnprotectData(
		&inBlob,
		nil,
		&entropyBlob,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&outBlob,
	); err != nil {
		return nil, fmt.Errorf("windows DPAPI CryptUnprotectData failed: %w", err)
	}
	if outBlob.Data != nil {
		defer func() {
			plainSlice := unsafe.Slice(outBlob.Data, int(outBlob.Size))
			zeroBytes(plainSlice)
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))
		}()
	}

	plain := append([]byte(nil), unsafe.Slice(outBlob.Data, int(outBlob.Size))...)
	return plain, nil
}

func (d *dpapiSecretStore) Delete(scope, id, ref string) error {
	// DPAPI sealed blob is stored inline in ref and removed atomically when persistence state updates.
	return nil
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
