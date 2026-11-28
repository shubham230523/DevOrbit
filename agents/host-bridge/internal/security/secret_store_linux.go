//go:build linux

package security

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	keyringServiceName = "devorbit-host-bridge"
	keyringRefPrefix   = "keyring-v1:"
	secretToolTimeout  = 3 * time.Second
)

type linuxSecretServiceStore struct{}

// NewDefaultSecretStore returns a Linux Secret Service (D-Bus / secret-tool) store.
// If D-Bus or secret-tool is unavailable (e.g. headless SSH/systemd without keyring),
// Available() returns false so callers fail closed without falling back to plaintext.
func NewDefaultSecretStore() SecretStore {
	return &linuxSecretServiceStore{}
}

func (l *linuxSecretServiceStore) Name() string {
	return "linux-secret-service"
}

func (l *linuxSecretServiceStore) Available() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return false
	}
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), secretToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "secret-tool", "lookup", "service", keyringServiceName, "scope", "probe", "id", "probe")
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	// secret-tool exits with code 1 when no item matches, which proves D-Bus Secret Service responded.
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true
	}
	return false
}

func (l *linuxSecretServiceStore) Store(scope, id string, secret []byte) (string, error) {
	if !l.Available() {
		return "", ErrSecureStoreUnavailable
	}
	if scope == "" || id == "" || len(secret) == 0 {
		return "", errors.New("scope, id, and non-empty secret are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), secretToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		"secret-tool", "store",
		"--label", "DevOrbit Host Bridge Session",
		"service", keyringServiceName,
		"scope", scope,
		"id", id,
	)
	cmd.Stdin = bytes.NewReader(secret)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("secret-tool store failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return keyringRefPrefix + scope + ":" + id, nil
}

func (l *linuxSecretServiceStore) Load(scope, id, ref string) ([]byte, error) {
	if !l.Available() {
		return nil, ErrSecureStoreUnavailable
	}
	if !strings.HasPrefix(ref, keyringRefPrefix) {
		return nil, ErrSecretNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), secretToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "secret-tool", "lookup", "service", keyringServiceName, "scope", scope, "id", id)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, ErrSecretNotFound
		}
		return nil, fmt.Errorf("secret-tool lookup failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	secret := bytes.TrimRight(stdout.Bytes(), "\r\n")
	if len(secret) == 0 {
		return nil, ErrSecretNotFound
	}
	return append([]byte(nil), secret...), nil
}

func (l *linuxSecretServiceStore) Delete(scope, id, ref string) error {
	if !l.Available() {
		return ErrSecureStoreUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), secretToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "secret-tool", "clear", "service", keyringServiceName, "scope", scope, "id", id)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secret-tool clear failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
