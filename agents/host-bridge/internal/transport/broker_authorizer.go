package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type BrokerAuthorizer interface {
	AuthorizeSession(ctx context.Context, machineID, deviceID, sessionID, clientToken, hostToken string, ttl time.Duration) error
	RevokeDevice(ctx context.Context, machineID, deviceID string) error
}

type HTTPBrokerAuthorizer struct {
	brokerURL   string
	adminSecret string
	httpClient  *http.Client
}

func NewHTTPBrokerAuthorizer(brokerURL, adminSecret string, httpClient *http.Client) *HTTPBrokerAuthorizer {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPBrokerAuthorizer{
		brokerURL:   brokerURL,
		adminSecret: adminSecret,
		httpClient:  httpClient,
	}
}

func (h *HTTPBrokerAuthorizer) AuthorizeSession(ctx context.Context, machineID, deviceID, sessionID, clientToken, hostToken string, ttl time.Duration) error {
	url := fmt.Sprintf("%s/api/v1/internal/sessions/authorize", h.brokerURL)
	payload := map[string]any{
		"machine_id":   machineID,
		"device_id":    deviceID,
		"session_id":   sessionID,
		"client_token": clientToken,
		"host_token":   hostToken,
		"ttl_seconds":  int64(ttl.Seconds()),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.adminSecret)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact broker authorization service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("broker authorization returned HTTP %d", resp.StatusCode)
	}

	return nil
}

func (h *HTTPBrokerAuthorizer) RevokeDevice(ctx context.Context, machineID, deviceID string) error {
	url := fmt.Sprintf("%s/api/v1/internal/devices/revoke", h.brokerURL)
	payload := map[string]string{
		"machine_id": machineID,
		"device_id":  deviceID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.adminSecret)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact broker revocation service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("broker revocation returned HTTP %d", resp.StatusCode)
	}

	return nil
}
