//go:build windows

package security

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const dpapiFileRefPrefix = "dpapi-file-v2:"

type dpapiSecretStore struct {
	baseDir string
}

// NewDefaultSecretStore returns a Windows DPAPI-backed SecretStore that stores
// each sealed session secret in an external per-session DPAPI-protected file under
// ~/.devorbit/secrets so individual session secrets can be deleted independently.
func NewDefaultSecretStore() SecretStore {
	return NewDPAPISecretStore("")
}

// NewDPAPISecretStore constructs a Windows DPAPI SecretStore rooted at customDir
// (or ~/.devorbit/secrets when customDir is empty).
func NewDPAPISecretStore(customDir string) SecretStore {
	dir := strings.TrimSpace(customDir)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = "."
		}
		dir = filepath.Join(home, ".devorbit", "secrets")
	}
	return &dpapiSecretStore{baseDir: dir}
}

func (d *dpapiSecretStore) Name() string {
	return "windows-dpapi-file-v2"
}

func (d *dpapiSecretStore) Available() bool {
	return d.baseDir != ""
}

func dpapiEntropyV2(scope, id, opaqueID string) []byte {
	return []byte("devorbit-secret-store-v2:" + scope + ":" + id + ":" + opaqueID)
}

func isSafeScope(scope string) bool {
	if scope == "" || len(scope) > 64 {
		return false
	}
	for i := 0; i < len(scope); i++ {
		c := scope[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func parseDPAPIFileRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, dpapiFileRefPrefix) {
		return "", ErrSecretNotFound
	}
	opaqueID := strings.TrimPrefix(ref, dpapiFileRefPrefix)
	if len(opaqueID) != 32 {
		return "", ErrSecretNotFound
	}
	for i := 0; i < len(opaqueID); i++ {
		c := opaqueID[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", ErrSecretNotFound
		}
	}
	return opaqueID, nil
}

func (d *dpapiSecretStore) secretPath(scope, opaqueID string) string {
	return filepath.Join(d.baseDir, scope+"_"+opaqueID+".dpapi")
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
	if !isSafeScope(scope) || id == "" || len(secret) == 0 {
		return "", errors.New("valid scope, id, and non-empty secret are required")
	}

	rawID := make([]byte, 16)
	if _, err := rand.Read(rawID); err != nil {
		return "", fmt.Errorf("failed to generate opaque secret reference: %w", err)
	}
	opaqueID := hex.EncodeToString(rawID)

	inCopy := append([]byte(nil), secret...)
	defer zeroBytes(inCopy)

	entropy := dpapiEntropyV2(scope, id, opaqueID)
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

	cipherBytes := append([]byte(nil), unsafe.Slice(outBlob.Data, int(outBlob.Size))...)
	secretFile := d.secretPath(scope, opaqueID)
	if err := WriteFileAtomic(secretFile, cipherBytes, 0600); err != nil {
		return "", fmt.Errorf("failed writing DPAPI secret file: %w", err)
	}

	return dpapiFileRefPrefix + opaqueID, nil
}

func (d *dpapiSecretStore) Load(scope, id, ref string) ([]byte, error) {
	if !isSafeScope(scope) || id == "" {
		return nil, errors.New("valid scope and id are required")
	}
	opaqueID, err := parseDPAPIFileRef(ref)
	if err != nil {
		return nil, err
	}

	secretFile := d.secretPath(scope, opaqueID)
	cipherBytes, err := os.ReadFile(secretFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSecretNotFound
		}
		return nil, fmt.Errorf("failed reading DPAPI secret file: %w", err)
	}
	if len(cipherBytes) == 0 {
		return nil, ErrSecretNotFound
	}

	entropy := dpapiEntropyV2(scope, id, opaqueID)
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

// Delete removes the external DPAPI-protected secret file so stale references can no
// longer unseal the secret. File removal removes OS-level access to the protected blob;
// it does not claim physical erasure of historical disk blocks.
func (d *dpapiSecretStore) Delete(scope, id, ref string) error {
	if ref == "" {
		return nil
	}
	if !isSafeScope(scope) || id == "" {
		return errors.New("valid scope and id are required")
	}
	opaqueID, err := parseDPAPIFileRef(ref)
	if err != nil {
		return err
	}
	secretFile := d.secretPath(scope, opaqueID)
	if err := os.Remove(secretFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed removing DPAPI secret file: %w", err)
	}
	return nil
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
