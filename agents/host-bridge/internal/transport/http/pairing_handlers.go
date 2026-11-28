package http

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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

func (s *Server) HandleCreatePairingChallenge(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeHostAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pairingEngine == nil {
		writeError(w, http.StatusInternalServerError, "pairing engine not initialized")
		return
	}

	clientIP := ExtractIP(r.RemoteAddr)
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

	var confirmBaseURL string
	if s.advertisedURL != "" {
		confirmBaseURL = s.advertisedURL
	} else {
		confirmBaseURL = os.Getenv("DEVORBIT_ADVERTISED_URL")
	}

	if s.isProduction {
		if confirmBaseURL == "" {
			writeError(w, http.StatusInternalServerError, "advertised URL must be configured in production mode")
			return
		}
		u, err := url.Parse(confirmBaseURL)
		if err != nil || u.Host == "" || strings.ToLower(u.Scheme) != "https" || u.User != nil {
			writeError(w, http.StatusInternalServerError, "invalid advertised URL in production: must be a valid https URL without credentials")
			return
		}
	} else {
		if confirmBaseURL == "" {
			// Check if requester IP matches trusted reverse proxies safely
			isTrustedProxy := IsTrustedProxy(r.RemoteAddr, s.trustedProxies)

			scheme := "http"
			host := ""

			if isTrustedProxy {
				if proto := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(proto, "https") {
					scheme = "https"
				}
				if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
					host = fwdHost
				}
			}

			if host == "" {
				host = r.Host
			}

			if host == "" || strings.ContainsAny(host, "/\\?#@ \t\r\n") {
				host = "127.0.0.1:8082"
			}
			confirmBaseURL = fmt.Sprintf("%s://%s", scheme, host)
		}
	}

	u, err := url.Parse(confirmBaseURL)
	if err != nil || u.Host == "" || u.User != nil || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") {
		writeError(w, http.StatusInternalServerError, "invalid advertised base URL: must be valid http or https URL without credentials")
		return
	}

	confirmBaseURL = strings.TrimRight(confirmBaseURL, "/")
	confirmURL := fmt.Sprintf("%s/api/v1/pairing/confirm", confirmBaseURL)

	brokerWSURL := s.resolveAdvertisedBrokerWSURL(r)

	writeJSON(w, http.StatusOK, map[string]any{
		"v":               ch.Version,
		"challenge_id":    ch.ID,
		"host_id":         ch.HostID,
		"host_name":       ch.HostName,
		"pairing_payload": ch.Payload,
		"pairing_secret":  ch.Secret,
		"confirm_url":     confirmURL,
		"broker_url":      brokerWSURL,
		"expires_at":      ch.ExpiresAtStr,
	})
}

func (s *Server) HandleConfirmPairing(w http.ResponseWriter, r *http.Request) {
	if s.pairingEngine == nil {
		writeError(w, http.StatusInternalServerError, "pairing engine not initialized")
		return
	}

	clientIP := ExtractIP(r.RemoteAddr)
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
		if s.rateLimiter != nil {
			s.rateLimiter.RecordFailure(clientIP)
		}
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

	// If this challenge+device was already confirmed and cached, return the exact same session
	// (only after ConfirmPairing already verified expiry + constant-time proof).
	s.pairingCacheMu.Lock()
	if s.pairingCache != nil {
		if cached, ok := s.pairingCache[req.ChallengeID+":"+req.DeviceID]; ok && cached.sessionToken == token {
			s.pairingCacheMu.Unlock()
			brokerWSURL := s.resolveAdvertisedBrokerWSURL(r)
			writeJSON(w, http.StatusOK, map[string]any{
				"status":         "success",
				"session_token":  cached.sessionToken,
				"session_id":     cached.sessionID,
				"session_secret": cached.sessionSecret,
				"machine_id":     s.pairingEngine.HostID(),
				"device_id":      req.DeviceID,
				"broker_url":     brokerWSURL,
				"expires_at":     cached.expiry.Format(time.RFC3339),
			})
			return
		}
	}
	s.pairingCacheMu.Unlock()

	// Generate a unique session ID and session-specific E2E master secret
	sessBytes := make([]byte, 16)
	if _, err := rand.Read(sessBytes); err != nil {
		s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
		writeError(w, http.StatusInternalServerError, "failed to generate random session ID")
		return
	}
	sessionID := fmt.Sprintf("sess-%s", hex.EncodeToString(sessBytes))

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
		writeError(w, http.StatusInternalServerError, "failed to generate random session secret")
		return
	}
	sessionSecret := hex.EncodeToString(secretBytes)

	// Authoritative UTC expiration timestamp calculated ONCE
	ttl := 24 * time.Hour
	expiry := time.Now().UTC().Add(ttl)

	// Register session in Host Bridge SessionStore first (enforces direct HTTP & Broker authN/authZ)
	store := s.ensureSessionStore()
	if _, err := store.RegisterSession(sessionID, req.DeviceID, token, sessionSecret, expiry, security.NewFullSessionPermissions()); err != nil {
		s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
		writeError(w, http.StatusInternalServerError, "failed to register host session: "+err.Error())
		return
	}

	// 1. Authorize session with Connection Broker using exact authoritative expiry timestamp
	if s.brokerAuthorizer != nil {
		err := s.brokerAuthorizer.AuthorizeSession(
			r.Context(),
			s.pairingEngine.HostID(),
			req.DeviceID,
			sessionID,
			token,
			s.hostToken,
			expiry,
		)
		if err != nil {
			store.RemoveSession(sessionID)
			s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
			writeError(w, http.StatusBadGateway, "failed to register session authorization with connection broker: "+err.Error())
			return
		}
	}

	// 2. Provision session on Host Bridge BrokerClient using exact authoritative expiry timestamp
	if s.sessionProvisioner != nil {
		if err := s.sessionProvisioner(sessionID, req.DeviceID, sessionSecret, expiry); err != nil {
			if s.brokerAuthorizer != nil {
				_ = s.brokerAuthorizer.RevokeDevice(r.Context(), s.pairingEngine.HostID(), req.DeviceID)
			}
			store.RemoveSession(sessionID)
			s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
			writeError(w, http.StatusInternalServerError, "failed to provision session on host bridge: "+err.Error())
			return
		}
	}

	s.pairingCacheMu.Lock()
	if s.pairingCache == nil {
		s.pairingCache = make(map[string]cachedPairingSession)
	}
	s.pairingCache[req.ChallengeID+":"+req.DeviceID] = cachedPairingSession{
		sessionToken:  token,
		sessionID:     sessionID,
		sessionSecret: sessionSecret,
		expiry:        expiry,
	}
	s.pairingCacheMu.Unlock()

	brokerWSURL := s.resolveAdvertisedBrokerWSURL(r)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "success",
		"session_token":  token,
		"session_id":     sessionID,
		"session_secret": sessionSecret,
		"machine_id":     s.pairingEngine.HostID(),
		"device_id":      req.DeviceID,
		"broker_url":     brokerWSURL,
		"expires_at":     expiry.Format(time.RFC3339),
	})
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

// resolveAdvertisedBrokerWSURL returns the WebSocket broker URL for the pairing
// client. When the configured URL is loopback (127.0.0.1/localhost) and the
// client reached the host bridge via a non-loopback LAN host, rewrite the
// broker host to match the request host so Wi-Fi clients don't get 127.0.0.1.
func (s *Server) resolveAdvertisedBrokerWSURL(r *http.Request) string {
	brokerWSURL := s.brokerWSURL
	if brokerWSURL == "" {
		brokerWSURL = s.brokerURL
	}
	if brokerWSURL == "" {
		brokerWSURL = os.Getenv("DEVORBIT_BROKER_WS_URL")
	}
	if brokerWSURL == "" {
		brokerWSURL = os.Getenv("DEVORBIT_BROKER_URL")
	}
	if brokerWSURL == "" {
		brokerWSURL = "ws://127.0.0.1:8081"
	}

	if r == nil || r.Host == "" {
		return brokerWSURL
	}

	reqHost := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		reqHost = h
	}
	if reqHost == "" || isLoopbackHost(reqHost) || strings.ContainsAny(reqHost, "/\\?#@ \t\r\n") {
		return brokerWSURL
	}

	parsed, err := url.Parse(brokerWSURL)
	if err != nil || !isLoopbackHost(parsed.Hostname()) {
		return brokerWSURL
	}

	if port := parsed.Port(); port != "" {
		parsed.Host = net.JoinHostPort(reqHost, port)
	} else {
		parsed.Host = reqHost
	}
	return parsed.String()
}
