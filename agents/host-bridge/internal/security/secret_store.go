package security

import "errors"

var (
	// ErrSecureStoreUnavailable indicates no OS-backed confidential secret store is accessible
	// in the current host environment. Callers must fail closed and never fall back to plaintext.
	ErrSecureStoreUnavailable = errors.New("OS secure secret store unavailable")

	// ErrSecretNotFound indicates a requested secret entry does not exist in the secure store.
	ErrSecretNotFound = errors.New("secret not found in OS secure store")
)

// SecretStore seals and retrieves sensitive session secrets using a platform-supported
// OS credential protection mechanism rather than plaintext files.
type SecretStore interface {
	Name() string
	Available() bool
	Store(scope, id string, secret []byte) (ref string, err error)
	Load(scope, id, ref string) ([]byte, error)
	Delete(scope, id, ref string) error
}
