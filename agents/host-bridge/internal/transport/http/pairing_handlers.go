package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"devorbit.net/agents/host-bridge/internal/security"
)

func (s *Server) SetPairingEngine(engine *security.PairingEngine) {
	s.pairingEngine = engine
}

func (s *Server) SetRateLimiter(limiter *security.RateLimiter) {
	s.rateLimiter = limiter
}

func (s *Server) SetSessionProvisioner(provisioner func(sessionID, deviceID, sessionSecret string) error) {
	s.sessionProvisioner = provisioner
}

func (s *Server) HandleCreatePairingChallenge(w http.ResponseWriter, r *http.Request) {
	if s.pairingEngine == nil {
		writeError(w, http.StatusInternalServerError, "pairing engine not initialized")
		return
	}

	clientIP := r.RemoteAddr
	if s.rateLimiter != nil {
		if err := s.rateLimiter.Allow(clientIP); err != nil {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
	}

	ch, err := s.pairingEngine.CreateChallenge(5 * time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	confirmBaseURL := s.advertisedURL
	if confirmBaseURL == "" {
		confirmBaseURL = os.Getenv("DEVORBIT_ADVERTISED_URL")
	}
	if confirmBaseURL == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		host := r.Host
		if host == "" {
			host = "127.0.0.1:3000"
		}
		confirmBaseURL = fmt.Sprintf("%s://%s", scheme, host)
	}
	confirmBaseURL = strings.TrimRight(confirmBaseURL, "/")
	confirmURL := fmt.Sprintf("%s/api/v1/pairing/confirm", confirmBaseURL)

	brokerURL := s.brokerURL
	if brokerURL == "" {
		brokerURL = os.Getenv("DEVORBIT_BROKER_URL")
	}
	if brokerURL == "" {
		brokerURL = "ws://127.0.0.1:8081"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"v":               ch.Version,
		"challenge_id":    ch.ID,
		"host_id":         ch.HostID,
		"host_name":       ch.HostName,
		"pairing_payload": ch.Payload,
		"pairing_secret":  ch.Secret,
		"confirm_url":     confirmURL,
		"broker_url":      brokerURL,
		"expires_at":      ch.ExpiresAtStr,
	})
}

func (s *Server) HandleConfirmPairing(w http.ResponseWriter, r *http.Request) {
	if s.pairingEngine == nil {
		writeError(w, http.StatusInternalServerError, "pairing engine not initialized")
		return
	}

	clientIP := r.RemoteAddr
	if s.rateLimiter != nil {
		if err := s.rateLimiter.Allow(clientIP); err != nil {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
	}

	var req struct {
		ChallengeID     string `json:"challenge_id"`
		DeviceID        string `json:"device_id"`
		DeviceName      string `json:"device_name"`
		DevicePublicKey string `json:"device_public_key"`
		ProofSignature  string `json:"proof_signature"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.rateLimiter != nil {
			s.rateLimiter.RecordFailure(clientIP)
		}
		writeError(w, http.StatusBadRequest, "invalid pairing confirm request JSON")
		return
	}

	if req.ChallengeID == "" || req.DeviceID == "" || req.ProofSignature == "" {
		writeError(w, http.StatusBadRequest, "challenge_id, device_id, and proof_signature are required")
		return
	}

	token, err := s.pairingEngine.ConfirmPairing(
		req.ChallengeID,
		req.DeviceID,
		req.DeviceName,
		req.DevicePublicKey,
		req.ProofSignature,
	)

	if err != nil {
		if s.rateLimiter != nil {
			s.rateLimiter.RecordFailure(clientIP)
		}
		if errors.Is(err, security.ErrPairingExpired) || errors.Is(err, security.ErrPairingUsed) || errors.Is(err, security.ErrInvalidProof) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if s.rateLimiter != nil {
		s.rateLimiter.RecordSuccess(clientIP)
	}

	// Generate a unique session ID and session-specific E2E master secret
	sessBytes := make([]byte, 16)
	if _, err := rand.Read(sessBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate random session ID")
		return
	}
	sessionID := fmt.Sprintf("sess-%s", hex.EncodeToString(sessBytes))

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate random session secret")
		return
	}
	sessionSecret := hex.EncodeToString(secretBytes)

	// 1. Authorize session with Connection Broker first
	if s.brokerAuthorizer != nil {
		err := s.brokerAuthorizer.AuthorizeSession(
			r.Context(),
			s.pairingEngine.HostID(),
			req.DeviceID,
			sessionID,
			token,
			s.hostToken,
			24*time.Hour,
		)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to register session authorization with connection broker: "+err.Error())
			return
		}
	}

	// 2. Connect verified pairing directly to Host Bridge session provisioning
	if s.sessionProvisioner != nil {
		if err := s.sessionProvisioner(sessionID, req.DeviceID, sessionSecret); err != nil {
			if s.brokerAuthorizer != nil {
				_ = s.brokerAuthorizer.RevokeDevice(r.Context(), s.pairingEngine.HostID(), req.DeviceID)
			}
			writeError(w, http.StatusInternalServerError, "failed to provision session on host bridge: "+err.Error())
			return
		}
	}

	brokerURL := s.brokerURL
	if brokerURL == "" {
		brokerURL = os.Getenv("DEVORBIT_BROKER_URL")
	}
	if brokerURL == "" {
		brokerURL = "ws://127.0.0.1:8081"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "success",
		"session_token":  token,
		"session_id":     sessionID,
		"session_secret": sessionSecret,
		"machine_id":     s.pairingEngine.HostID(),
		"device_id":      req.DeviceID,
		"broker_url":     brokerURL,
		"expires_at":     time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
}
