//go:build !windows && !linux

package security

type unsupportedSecretStore struct{}

func NewDefaultSecretStore() SecretStore {
	return &unsupportedSecretStore{}
}

func (u *unsupportedSecretStore) Name() string {
	return "unsupported"
}

func (u *unsupportedSecretStore) Available() bool {
	return false
}

func (u *unsupportedSecretStore) Store(scope, id string, secret []byte) (string, error) {
	return "", ErrSecureStoreUnavailable
}

func (u *unsupportedSecretStore) Load(scope, id, ref string) ([]byte, error) {
	return nil, ErrSecureStoreUnavailable
}

func (u *unsupportedSecretStore) Delete(scope, id, ref string) error {
	return ErrSecureStoreUnavailable
}
