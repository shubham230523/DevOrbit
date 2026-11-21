package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

	writeJSON(w, http.StatusOK, map[string]any{
		"v":               "1.0",
		"challenge_id":    ch.ID,
		"host_id":         ch.HostID,
		"host_name":       ch.HostName,
		"pairing_payload": ch.Payload,
		"pairing_secret":  ch.Secret,
		"confirm_url":     "/api/v1/pairing/confirm",
		"expires_at":      ch.ExpiresAt.Format(time.RFC3339),
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
	_, _ = rand.Read(sessBytes)
	sessionID := fmt.Sprintf("sess-%s", hex.EncodeToString(sessBytes))

	secretBytes := make([]byte, 32)
	_, _ = rand.Read(secretBytes)
	sessionSecret := hex.EncodeToString(secretBytes)

	// Connect verified pairing directly to Host Bridge session provisioning
	if s.sessionProvisioner != nil {
		if err := s.sessionProvisioner(sessionID, req.DeviceID, sessionSecret); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to provision session on host bridge: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "success",
		"session_token":  token,
		"session_id":     sessionID,
		"session_secret": sessionSecret,
		"machine_id":     s.pairingEngine.HostID(),
		"device_id":      req.DeviceID,
		"expires_at":     time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
}
