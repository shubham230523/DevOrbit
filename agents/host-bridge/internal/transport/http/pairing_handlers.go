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
	"devorbit.net/services/connection-broker/pkg/observability"
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

// BuildStartupPairingQRPayload constructs the startup pairing QR payload displayed in the Host Bridge CLI.
// In production mode it requires native TLS or configured trusted reverse proxies and always emits the
// configured HTTPS DEVORBIT_ADVERTISED_URL rather than an unencrypted LAN URL.
func (s *Server) BuildStartupPairingQRPayload(ch *security.PairingChallenge, localIP string, port int, hasNativeTLS bool) (map[string]any, error) {
	if ch == nil {
		return nil, errors.New("pairing challenge cannot be nil")
	}

	var confirmBaseURL string
	if s.advertisedURL != "" {
		confirmBaseURL = s.advertisedURL
	} else {
		confirmBaseURL = os.Getenv("DEVORBIT_ADVERTISED_URL")
	}

	if s.isProduction {
		if !hasNativeTLS && len(s.trustedProxies) == 0 {
			return nil, errors.New("production mode requires native TLS or trusted reverse proxy")
		}
		if confirmBaseURL == "" {
			return nil, errors.New("advertised URL must be configured in production mode")
		}
		u, err := url.Parse(confirmBaseURL)
		if err != nil || u.Host == "" || strings.ToLower(u.Scheme) != "https" || u.User != nil {
			return nil, errors.New("invalid advertised URL in production: must be a valid https URL without credentials")
		}
	} else if confirmBaseURL == "" {
		scheme := "http"
		if hasNativeTLS {
			scheme = "https"
		}
		if localIP == "" {
			localIP = "127.0.0.1"
		}
		confirmBaseURL = fmt.Sprintf("%s://%s:%d", scheme, localIP, port)
	}

	u, err := url.Parse(confirmBaseURL)
	if err != nil || u.Host == "" || u.User != nil || (strings.ToLower(u.Scheme) != "http" && strings.ToLower(u.Scheme) != "https") {
		return nil, errors.New("invalid advertised base URL: must be valid http or https URL without credentials")
	}

	confirmBaseURL = strings.TrimRight(confirmBaseURL, "/")
	confirmURL := fmt.Sprintf("%s/api/v1/pairing/confirm", confirmBaseURL)
	brokerWSURL := s.resolveAdvertisedBrokerWSURL(nil)

	return map[string]any{
		"v":               ch.Version,
		"challenge_id":    ch.ID,
		"host_id":         ch.HostID,
		"host_name":       ch.HostName,
		"pairing_payload": ch.Payload,
		"pairing_secret":  ch.Secret,
		"confirm_url":     confirmURL,
		"broker_url":      brokerWSURL,
		"expires_at":      ch.ExpiresAtStr,
	}, nil
}

func (s *Server) HandleConfirmPairing(w http.ResponseWriter, r *http.Request) {
	if s.isProduction && !s.isTransportConfidential(r) {
		writeError(w, http.StatusForbidden, "pairing confirmation requires HTTPS or trusted reverse proxy in production")
		return
	}
	if s.pairingEngine == nil {
		writeError(w, http.StatusInternalServerError, "pairing engine not initialized")
		return
	}

	clientIP := ExtractIP(r.RemoteAddr)
	if s.rateLimiter != nil {
		if err := s.rateLimiter.Allow(clientIP); err != nil {
			s.securityEvent(observability.LevelWarn, EventRateLimited, map[string]any{
				"operation": "pairing_confirm",
				"client_ip": clientIP,
			})
			s.incrementMetric("devorbit_pairing_rate_limited_total")
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
	}

	var req struct {
		ChallengeID string `json:"challenge_id"`
		DeviceID    string `json:"device_id"`
		DeviceName  string `json:"device_name"`
		// DevicePublicKey is accepted for wire compatibility only and is not used cryptographically
		// by Host Bridge; response confidentiality is enforced by HTTPS/TLS transport.
		DevicePublicKey string `json:"device_public_key"`
		Timestamp       string `json:"timestamp"`
		Nonce           string `json:"nonce"`
		ProofSignature  string `json:"proof_signature"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if s.rateLimiter != nil {
			s.rateLimiter.RecordFailure(clientIP)
		}
		writeError(w, http.StatusBadRequest, "invalid pairing confirm request JSON")
		return
	}

	if req.ChallengeID == "" || req.DeviceID == "" || req.Timestamp == "" || req.Nonce == "" || req.ProofSignature == "" {
		if s.rateLimiter != nil {
			s.rateLimiter.RecordFailure(clientIP)
		}
		writeError(w, http.StatusBadRequest, "challenge_id, device_id, timestamp, nonce, and proof_signature are required")
		return
	}

	token, _, _, err := s.pairingEngine.ConfirmPairing(
		req.ChallengeID,
		req.DeviceID,
		req.DeviceName,
		req.Timestamp,
		req.Nonce,
		req.ProofSignature,
	)

	if err != nil {
		if s.rateLimiter != nil {
			s.rateLimiter.RecordFailure(clientIP)
		}
		// Security event: never include the proof, nonce or secrets; reason only.
		s.securityEvent(observability.LevelWarn, EventPairingFailed, map[string]any{
			"reason":    pairingFailureReason(err),
			"client_ip": clientIP,
			"device_id": req.DeviceID,
		})
		s.incrementMetric("devorbit_pairing_failures_total")
		if errors.Is(err, security.ErrReplayDetected) {
			writeError(w, http.StatusForbidden, err.Error())
		} else if errors.Is(err, security.ErrPairingExpired) ||
			errors.Is(err, security.ErrPairingUsed) ||
			errors.Is(err, security.ErrInvalidProof) ||
			errors.Is(err, security.ErrInvalidTimestamp) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if s.rateLimiter != nil {
		s.rateLimiter.RecordSuccess(clientIP)
	}

	s.securityEvent(observability.LevelInfo, EventPairingSucceeded, map[string]any{
		"client_ip": clientIP,
		"device_id": req.DeviceID,
	})
	s.incrementMetric("devorbit_pairing_success_total")

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
		if rbErr := store.RemoveSession(sessionID); rbErr != nil {
			s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to register host session: %v (rollback error: %v)", err, rbErr))
			return
		}
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
			rbErr := store.RemoveSession(sessionID)
			s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
			if rbErr != nil {
				writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to register session authorization with connection broker: %v (rollback error: %v)", err, rbErr))
				return
			}
			writeError(w, http.StatusBadGateway, "failed to register session authorization with connection broker: "+err.Error())
			return
		}
	}

	// 2. Provision session on Host Bridge BrokerClient using exact authoritative expiry timestamp
	if s.sessionProvisioner != nil {
		if err := s.sessionProvisioner(sessionID, req.DeviceID, sessionSecret, expiry); err != nil {
			var rollbackErrs []error
			if s.sessionRevoker != nil {
				if revErr := s.sessionRevoker(sessionID, ""); revErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("broker client rollback: %w", revErr))
				}
			}
			if s.brokerAuthorizer != nil {
				if authRevErr := s.brokerAuthorizer.RevokeDevice(r.Context(), s.pairingEngine.HostID(), req.DeviceID); authRevErr != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("remote broker rollback: %w", authRevErr))
				}
			}
			if rmErr := store.RemoveSession(sessionID); rmErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("local store rollback: %w", rmErr))
			}
			s.pairingEngine.UnconsumeChallenge(req.ChallengeID)
			if len(rollbackErrs) > 0 {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to provision session on host bridge: %v (rollback errors: %v)", err, errors.Join(rollbackErrs...)))
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to provision session on host bridge: "+err.Error())
			return
		}
	}

	s.pairingEngine.BindChallengeSession(req.ChallengeID, sessionID)

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
