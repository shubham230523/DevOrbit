package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"devorbit.net/agents/host-bridge/internal/security"
)

const (
	HeaderSessionID = "X-DevOrbit-Session-ID"
	HeaderDeviceID  = "X-DevOrbit-Device-ID"
	HeaderTimestamp = "X-DevOrbit-Timestamp"
	HeaderNonce     = "X-DevOrbit-Nonce"
	HeaderSignature = "X-DevOrbit-Signature"

	maxAuthBodyBytes = 32 * 1024 * 1024 // 32 MB
)

type authContextKey struct{}

// AuthenticatedContext represents a verified caller identity bound to an active session and device.
type AuthenticatedContext struct {
	Session               *security.SessionRecord
	SessionID             string
	DeviceID              string
	ViaE2E                bool
	ViaBroker             bool
	TransportConfidential bool
}

// ContextWithAuthenticatedSession attaches a verified AuthenticatedContext to ctx.
// Because the key is unexported, external HTTP headers or callers cannot forge it.
func ContextWithAuthenticatedSession(ctx context.Context, sess *security.SessionRecord, viaE2E, viaBroker bool) context.Context {
	if sess == nil {
		return ctx
	}
	return context.WithValue(ctx, authContextKey{}, &AuthenticatedContext{
		Session:               sess,
		SessionID:             sess.SessionID,
		DeviceID:              sess.DeviceID,
		ViaE2E:                viaE2E,
		ViaBroker:             viaBroker,
		TransportConfidential: viaE2E || viaBroker,
	})
}

func authenticatedFromContext(ctx context.Context) (*AuthenticatedContext, bool) {
	if ctx == nil {
		return nil, false
	}
	ac, ok := ctx.Value(authContextKey{}).(*AuthenticatedContext)
	if !ok || ac == nil || ac.Session == nil {
		return nil, false
	}
	return ac, true
}

type e2eFrameEnvelope struct {
	ProtocolVersion string `json:"v"`
	SessionID       string `json:"session_id"`
	Type            string `json:"type"`
	EncryptedData   string `json:"encrypted_payload"`
	SeqNum          int64  `json:"seq_num"`
	Timestamp       int64  `json:"timestamp"`
}

type e2eAppRequestEnvelope struct {
	ProtocolVersion string            `json:"v"`
	SessionID       string            `json:"session_id"`
	RequestID       string            `json:"request_id"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	SeqNum          int64             `json:"seq_num"`
	Timestamp       int64             `json:"timestamp"`
}

type e2eAppResponseEnvelope struct {
	ProtocolVersion string            `json:"v"`
	SessionID       string            `json:"session_id"`
	RequestID       string            `json:"request_id"`
	StatusCode      int               `json:"status_code"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	SeqNum          int64             `json:"seq_num"`
	Timestamp       int64             `json:"timestamp"`
}

var (
	errQueryCreds    = errors.New("credentials supplied via URL query parameters are rejected")
	errMissingBearer = errors.New("missing or malformed Bearer authorization header")
)

func hasForbiddenQueryCredentials(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	q := r.URL.Query()
	for _, forbiddenKey := range []string{"token", "credential", "secret", "session_token", "session_secret", "api_key"} {
		if q.Has(forbiddenKey) {
			return true
		}
	}
	return false
}

func extractBearerToken(r *http.Request) (string, error) {
	if hasForbiddenQueryCredentials(r) {
		return "", errQueryCreds
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return "", errMissingBearer
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errMissingBearer
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errMissingBearer
	}
	return token, nil
}

func readBoundedRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	limited := io.LimitReader(r.Body, maxAuthBodyBytes+1)
	bodyBytes, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return nil, false
	}
	if len(bodyBytes) > maxAuthBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds maximum allowed size")
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, true
}

func verifySignedHMACAndConsumeNonce(w http.ResponseWriter, r *http.Request, rec *security.SessionRecord, bodyBytes []byte) bool {
	tsRaw := strings.TrimSpace(r.Header.Get(HeaderTimestamp))
	nonce := strings.TrimSpace(r.Header.Get(HeaderNonce))
	sig := strings.TrimSpace(r.Header.Get(HeaderSignature))
	if tsRaw == "" || nonce == "" || sig == "" {
		writeError(w, http.StatusUnauthorized, "missing required request signature, timestamp, or nonce headers")
		return false
	}

	tsTime, tsValid := security.ParseTimestamp(tsRaw)
	if !tsValid || !security.IsTimestampFresh(tsTime, time.Now().UTC()) {
		writeError(w, http.StatusUnauthorized, security.ErrInvalidTimestamp.Error())
		return false
	}

	bodyHash := sha256.Sum256(bodyBytes)
	bodyHashHex := hex.EncodeToString(bodyHash[:])

	expectedSig := security.ComputeHTTPRequestMAC(
		rec.HTTPAuthKey,
		r.Method,
		r.URL.RequestURI(),
		rec.SessionID,
		rec.DeviceID,
		tsRaw,
		nonce,
		bodyHashHex,
	)

	if subtle.ConstantTimeCompare([]byte(sig), []byte(expectedSig)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid request signature")
		return false
	}

	if err := rec.VerifyAndConsumeNonce(nonce, tsTime, time.Now().UTC()); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return false
	}
	return true
}

func (s *Server) isTransportConfidential(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if IsTrustedProxy(r.RemoteAddr, s.trustedProxies) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

func (s *Server) ensureSessionStore() *security.SessionStore {
	s.sessionStoreMu.Lock()
	defer s.sessionStoreMu.Unlock()
	if s.sessionStore == nil {
		s.sessionStore = security.NewSessionStore()
		s.wireSessionStoreHooks(s.sessionStore)
	}
	return s.sessionStore
}

func (s *Server) authenticateDirectSessionHeaders(w http.ResponseWriter, r *http.Request) (*security.SessionRecord, bool) {
	token, err := extractBearerToken(r)
	if err != nil {
		if errors.Is(err, errQueryCreds) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusUnauthorized, err.Error())
		}
		return nil, false
	}

	claimedSessionID := strings.TrimSpace(r.Header.Get(HeaderSessionID))
	claimedDeviceID := strings.TrimSpace(r.Header.Get(HeaderDeviceID))
	if claimedSessionID == "" || claimedDeviceID == "" {
		writeError(w, http.StatusUnauthorized, "missing required session and device binding headers")
		return nil, false
	}

	store := s.ensureSessionStore()
	rec, err := store.AuthenticateBearer(token, claimedSessionID, claimedDeviceID)
	if err != nil {
		switch {
		case errors.Is(err, security.ErrSessionRevoked), errors.Is(err, security.ErrSessionBindingMismatch):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeError(w, http.StatusUnauthorized, err.Error())
		}
		return nil, false
	}
	return rec, true
}

func (s *Server) authorizeRequest(w http.ResponseWriter, r *http.Request, requiredPerm security.ActionPermission) (*AuthenticatedContext, bool) {
	store := s.ensureSessionStore()

	if ac, ok := authenticatedFromContext(r.Context()); ok {
		// Re-verify active status (non-revoked, non-expired) on every invocation.
		rec, err := store.GetActiveSession(ac.SessionID, ac.DeviceID)
		if err != nil {
			if errors.Is(err, security.ErrSessionRevoked) || errors.Is(err, security.ErrSessionBindingMismatch) {
				writeError(w, http.StatusForbidden, err.Error())
			} else {
				writeError(w, http.StatusUnauthorized, err.Error())
			}
			return nil, false
		}
		if !rec.IsAuthorized(requiredPerm) {
			writeError(w, http.StatusForbidden, "insufficient session permissions for operation")
			return nil, false
		}
		store.MarkSessionUsed(rec.SessionID)
		ac.Session = rec
		return ac, true
	}

	rec, ok := s.authenticateDirectSessionHeaders(w, r)
	if !ok {
		return nil, false
	}

	if !s.isTransportConfidential(r) {
		writeError(w, http.StatusForbidden, "plaintext HTTP rejected for protected endpoints: use E2E encryption (/api/v1/e2e) or HTTPS")
		return nil, false
	}

	bodyBytes, ok := readBoundedRequestBody(w, r)
	if !ok {
		return nil, false
	}
	if !verifySignedHMACAndConsumeNonce(w, r, rec, bodyBytes) {
		return nil, false
	}

	if !rec.IsAuthorized(requiredPerm) {
		writeError(w, http.StatusForbidden, "insufficient session permissions for operation")
		return nil, false
	}

	store.MarkSessionUsed(rec.SessionID)

	return &AuthenticatedContext{
		Session:               rec,
		SessionID:             rec.SessionID,
		DeviceID:              rec.DeviceID,
		ViaE2E:                false,
		ViaBroker:             false,
		TransportConfidential: true,
	}, true
}

func (s *Server) tryAuthenticateOptional(r *http.Request) (*AuthenticatedContext, bool) {
	store := s.ensureSessionStore()
	if ac, ok := authenticatedFromContext(r.Context()); ok {
		rec, err := store.GetActiveSession(ac.SessionID, ac.DeviceID)
		if err != nil {
			return nil, false
		}
		ac.Session = rec
		return ac, true
	}
	return nil, false
}

func (s *Server) authorizeHostAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.isProduction && !s.isTransportConfidential(r) {
		writeError(w, http.StatusForbidden, "host-admin operations require HTTPS or trusted reverse proxy in production")
		return false
	}
	if s.hostToken == "" {
		writeError(w, http.StatusUnauthorized, "host admin secret not configured")
		return false
	}
	token, err := extractBearerToken(r)
	if err != nil {
		if errors.Is(err, errQueryCreds) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusUnauthorized, err.Error())
		}
		return false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.hostToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid host admin credentials")
		return false
	}
	return true
}

func (s *Server) withAuth(method string, perm security.ActionPermission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ac, ok := s.authorizeRequest(w, r, perm)
		if !ok {
			return
		}
		if method != "" && r.Method != method {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx := ContextWithAuthenticatedSession(r.Context(), ac.Session, ac.ViaE2E, ac.ViaBroker)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) withHostAdmin(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeHostAdmin(w, r) {
			return
		}
		if method != "" && r.Method != method {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func validateTunneledRequest(appPath, appMethod, expectedSessionID, expectedDeviceID string, headers map[string]string) (string, error) {
	switch appMethod {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
	default:
		return "", errors.New("unsupported HTTP method in envelope")
	}

	if appPath == "" || !strings.HasPrefix(appPath, "/api/v1/") {
		return "", errors.New("tunneled path must start with /api/v1/")
	}
	if strings.Contains(appPath, "\\") || strings.Contains(appPath, "#") || strings.Contains(appPath, "..") {
		return "", errors.New("invalid characters or traversal in tunneled path")
	}

	pathOnly := appPath
	if idx := strings.IndexByte(appPath, '?'); idx >= 0 {
		pathOnly = appPath[:idx]
	}
	cleaned := path.Clean(pathOnly)
	if cleaned != pathOnly && !(strings.HasSuffix(pathOnly, "/") && cleaned+"/" == pathOnly) {
		return "", errors.New("non-canonical path in tunneled request")
	}
	if cleaned == "/api/v1/e2e" {
		return "", errors.New("nested E2E tunnel dispatch is not allowed")
	}

	for k, v := range headers {
		if strings.EqualFold(k, HeaderSessionID) && strings.TrimSpace(v) != "" && strings.TrimSpace(v) != expectedSessionID {
			return "", security.ErrSessionBindingMismatch
		}
		if strings.EqualFold(k, HeaderDeviceID) && strings.TrimSpace(v) != "" && strings.TrimSpace(v) != expectedDeviceID {
			return "", security.ErrSessionBindingMismatch
		}
	}
	return pathOnly, nil
}

func applySafeTunneledHeaders(dst http.Header, src map[string]string) {
	for k, v := range src {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "" ||
			lk == "authorization" ||
			lk == "host" ||
			strings.HasPrefix(lk, "x-forwarded-") ||
			lk == "forwarded" ||
			strings.HasPrefix(lk, "x-devorbit-") {
			continue
		}
		dst.Set(k, v)
	}
}

// authenticateDirectE2ESession authenticates an outer E2E HTTP tunnel request using HKDF-derived
// HMAC-SHA256 request signatures bound to session_id and device_id, without transmitting raw bearer tokens.
func (s *Server) authenticateDirectE2ESession(w http.ResponseWriter, r *http.Request) (*security.SessionRecord, []byte, bool) {
	if hasForbiddenQueryCredentials(r) {
		writeError(w, http.StatusBadRequest, errQueryCreds.Error())
		return nil, nil, false
	}
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		writeError(w, http.StatusUnauthorized, "raw bearer Authorization header is rejected on /api/v1/e2e; authenticate with HMAC proof headers")
		return nil, nil, false
	}

	claimedSessionID := strings.TrimSpace(r.Header.Get(HeaderSessionID))
	claimedDeviceID := strings.TrimSpace(r.Header.Get(HeaderDeviceID))
	if claimedSessionID == "" || claimedDeviceID == "" {
		writeError(w, http.StatusUnauthorized, "missing required session and device binding headers")
		return nil, nil, false
	}

	store := s.ensureSessionStore()
	rec, err := store.GetActiveSession(claimedSessionID, claimedDeviceID)
	if err != nil {
		switch {
		case errors.Is(err, security.ErrSessionRevoked), errors.Is(err, security.ErrSessionBindingMismatch):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeError(w, http.StatusUnauthorized, err.Error())
		}
		return nil, nil, false
	}
	if len(rec.HTTPAuthKey) == 0 {
		writeError(w, http.StatusUnauthorized, "HTTP auth key unavailable for session")
		return nil, nil, false
	}

	bodyBytes, ok := readBoundedRequestBody(w, r)
	if !ok {
		return nil, nil, false
	}
	if !verifySignedHMACAndConsumeNonce(w, r, rec, bodyBytes) {
		return nil, nil, false
	}
	return rec, bodyBytes, true
}

// HandleE2ERequest processes an authenticated AES-256-GCM encrypted HTTP envelope over LAN/HTTPS.
func (s *Server) HandleE2ERequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rec, bodyBytes, ok := s.authenticateDirectE2ESession(w, r)
	if !ok {
		return
	}

	if rec.DirectHTTPE2E == nil {
		writeError(w, http.StatusUnauthorized, "E2E session keys unavailable")
		return
	}

	var frame e2eFrameEnvelope
	if err := json.Unmarshal(bodyBytes, &frame); err != nil {
		writeError(w, http.StatusBadRequest, "invalid E2E frame envelope JSON")
		return
	}

	if frame.Type != "data" || frame.EncryptedData == "" {
		writeError(w, http.StatusBadRequest, "invalid E2E frame type or empty payload")
		return
	}
	if frame.SessionID != rec.SessionID {
		writeError(w, http.StatusForbidden, security.ErrSessionBindingMismatch.Error())
		return
	}

	frameTS, validTS := security.ParseUnixTimestampInt64(frame.Timestamp)
	if !validTS || !security.IsTimestampFresh(frameTS, time.Now().UTC()) {
		writeError(w, http.StatusUnauthorized, security.ErrInvalidTimestamp.Error())
		return
	}

	plainReqBytes, err := rec.DirectHTTPE2E.Decrypt(security.DirClientToHost, frame.EncryptedData, frame.SeqNum, frame.Timestamp)
	if err != nil {
		if errors.Is(err, security.ErrReplayedFrame) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusUnauthorized, err.Error())
		}
		return
	}

	s.ensureSessionStore().MarkSessionUsed(rec.SessionID)

	var appReq e2eAppRequestEnvelope
	if err := json.Unmarshal(plainReqBytes, &appReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid decrypted application request envelope")
		return
	}

	if appReq.SessionID != rec.SessionID {
		writeError(w, http.StatusForbidden, security.ErrSessionBindingMismatch.Error())
		return
	}
	if appReq.SeqNum != frame.SeqNum || appReq.Timestamp != frame.Timestamp {
		writeError(w, http.StatusForbidden, "envelope metadata mismatch")
		return
	}

	if _, err := validateTunneledRequest(appReq.Path, appReq.Method, rec.SessionID, rec.DeviceID, appReq.Headers); err != nil {
		if errors.Is(err, security.ErrSessionBindingMismatch) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	innerReq, err := http.NewRequestWithContext(
		ContextWithAuthenticatedSession(r.Context(), rec, true, false),
		appReq.Method,
		appReq.Path,
		strings.NewReader(appReq.Body),
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid inner request")
		return
	}
	applySafeTunneledHeaders(innerReq.Header, appReq.Headers)

	buf := newInMemoryResponseWriter()
	s.Mux().ServeHTTP(buf, innerReq)

	respHeaders := make(map[string]string)
	if ct := buf.Header().Get("Content-Type"); ct != "" {
		respHeaders["Content-Type"] = ct
	}

	respTS := time.Now().UTC().UnixMicro()
	appResp := e2eAppResponseEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       rec.SessionID,
		RequestID:       appReq.RequestID,
		StatusCode:      buf.code,
		Headers:         respHeaders,
		Body:            buf.body.String(),
		SeqNum:          frame.SeqNum,
		Timestamp:       respTS,
	}

	respPlain, err := json.Marshal(appResp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize response envelope")
		return
	}

	encryptedResp, err := rec.DirectHTTPE2E.Encrypt(security.DirHostToClient, respPlain, frame.SeqNum, respTS)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encrypt response envelope")
		return
	}

	writeJSON(w, http.StatusOK, e2eFrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       rec.SessionID,
		Type:            "data",
		EncryptedData:   encryptedResp,
		SeqNum:          frame.SeqNum,
		Timestamp:       respTS,
	})
}

// ServeBrokerRequest dispatches an E2E-decrypted request arriving from the Connection Broker tunnel
// through the exact same Host Bridge session, revocation, expiry, permission, and ownership checks.
func (s *Server) ServeBrokerRequest(w http.ResponseWriter, r *http.Request, sessionID, deviceID string, headers map[string]string) bool {
	store := s.ensureSessionStore()
	rec, err := store.GetActiveSession(sessionID, deviceID)
	if err != nil {
		if errors.Is(err, security.ErrSessionRevoked) || errors.Is(err, security.ErrSessionBindingMismatch) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusUnauthorized, err.Error())
		}
		return false
	}

	if _, err := validateTunneledRequest(r.URL.RequestURI(), r.Method, rec.SessionID, rec.DeviceID, headers); err != nil {
		if errors.Is(err, security.ErrSessionBindingMismatch) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return false
	}

	applySafeTunneledHeaders(r.Header, headers)
	ctx := ContextWithAuthenticatedSession(r.Context(), rec, true, true)
	s.Mux().ServeHTTP(w, r.WithContext(ctx))
	return true
}

type inMemoryResponseWriter struct {
	code        int
	headerMap   http.Header
	body        bytes.Buffer
	wroteHeader bool
}

func newInMemoryResponseWriter() *inMemoryResponseWriter {
	return &inMemoryResponseWriter{
		code:      http.StatusOK,
		headerMap: make(http.Header),
	}
}

func (rw *inMemoryResponseWriter) Header() http.Header {
	return rw.headerMap
}

func (rw *inMemoryResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.body.Write(b)
}

func (rw *inMemoryResponseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.code = code
	rw.wroteHeader = true
}
