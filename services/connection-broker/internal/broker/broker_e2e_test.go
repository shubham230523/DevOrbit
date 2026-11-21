package broker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/hkdf"
)

func deriveTestKeys(masterSecret, sessionID, deviceID string) ([]byte, []byte, error) {
	secretBytes := []byte(masterSecret)
	salt := []byte(fmt.Sprintf("devorbit-e2e-salt-%s", sessionID))
	infoClient := []byte(fmt.Sprintf("devorbit-client-to-host-%s-%s", sessionID, deviceID))
	infoHost := []byte(fmt.Sprintf("devorbit-host-to-client-%s-%s", sessionID, deviceID))

	hkdfClient := hkdf.New(sha256.New, secretBytes, salt, infoClient)
	cKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdfClient, cKey); err != nil {
		return nil, nil, err
	}

	hkdfHost := hkdf.New(sha256.New, secretBytes, salt, infoHost)
	hKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdfHost, hKey); err != nil {
		return nil, nil, err
	}

	return cKey, hKey, nil
}

func constructTestAAD(sessionID, deviceID string, seqNum, timestamp int64) []byte {
	aad := make([]byte, 0, 64)
	aad = append(aad, []byte(sessionID)...)
	aad = append(aad, ':')
	aad = append(aad, []byte(deviceID)...)
	aad = append(aad, ':')

	seqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(seqBytes, uint64(seqNum))
	aad = append(aad, seqBytes...)

	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(timestamp))
	aad = append(aad, tsBytes...)

	return aad
}

func encryptTestPayload(key []byte, nonce []byte, plain []byte, aad []byte) string {
	block, _ := aes.NewCipher(key)
	aesGCM, _ := cipher.NewGCM(block)
	sealed := aesGCM.Seal(nil, nonce, plain, aad)
	full := append(nonce, sealed...)
	return hex.EncodeToString(full)
}

func decryptTestPayload(key []byte, cipherHex string, aad []byte) ([]byte, error) {
	raw, err := hex.DecodeString(cipherHex)
	if err != nil {
		return nil, err
	}
	if len(raw) < 28 {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, actualCipher := raw[:12], raw[12:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aesGCM.Open(nil, nonce, actualCipher, aad)
}

func TestBroker_AuthenticatedEndToEndWebSocketRelay(t *testing.T) {
	b := NewBroker()
	machineID := "mach-e2e-01"
	deviceID := "dev-phone-01"
	sessionID := "sess-e2e-test-100"
	hostToken := "host-auth-token-secret-32b"
	clientToken := "client-auth-token-secret-32b"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// 1. Host Bridge dials /ws/host with valid Authorization header (NO token in query string)
	hostDialURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeaders := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, _, err := websocket.DefaultDialer.Dial(hostDialURL, hostHeaders)
	if err != nil {
		t.Fatalf("Failed host WSS connection: %v", err)
	}
	defer hostConn.Close()

	// 2. Client dials /ws/client with valid Authorization header (NO token in query string)
	clientDialURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeaders := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, _, err := websocket.DefaultDialer.Dial(clientDialURL, clientHeaders)
	if err != nil {
		t.Fatalf("Failed client WSS connection: %v", err)
	}
	defer clientConn.Close()

	time.Sleep(50 * time.Millisecond)

	// 3. Client sends E2E Encrypted Payload Frame to Broker -> Broker Relays to Host
	opaqueCiphertext := "a1b2c3d4_opaque_e2e_encrypted_bytes_here"
	clientReqFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   opaqueCiphertext,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := clientConn.WriteJSON(&clientReqFrame); err != nil {
		t.Fatalf("Failed to send frame from client: %v", err)
	}

	// Host reads relayed frame from broker
	var hostFrame FrameEnvelope
	if err := hostConn.ReadJSON(&hostFrame); err != nil {
		t.Fatalf("Host failed to read relayed frame: %v", err)
	}

	if hostFrame.EncryptedData != opaqueCiphertext {
		t.Errorf("Opaque relay failed! Host received '%s', expected '%s'", hostFrame.EncryptedData, opaqueCiphertext)
	}

	// 4. Host sends encrypted response back to client via broker
	opaqueRespCiphertext := "e5f6g7h8_opaque_e2e_encrypted_response_bytes"
	hostRespFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   opaqueRespCiphertext,
		SeqNum:          1,
		Timestamp:       time.Now().UnixNano(),
	}

	if err := hostConn.WriteJSON(&hostRespFrame); err != nil {
		t.Fatalf("Host failed to send response frame: %v", err)
	}

	var clientFrame FrameEnvelope
	if err := clientConn.ReadJSON(&clientFrame); err != nil {
		t.Fatalf("Client failed to read response frame: %v", err)
	}

	if clientFrame.EncryptedData != opaqueRespCiphertext {
		t.Errorf("Client response mismatch! Expected '%s', got '%s'", opaqueRespCiphertext, clientFrame.EncryptedData)
	}

	// 5. Test Revocation Teardown
	b.RevokeDevice(machineID, deviceID)
	time.Sleep(50 * time.Millisecond)

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected active sessions to be 0 after revocation")
	}
}

// TestBroker_CrossComponentEndToEndSecurity verifies:
// - Query-token rejection
// - Header authentication
// - Cryptographically derived directional keys
// - Real encrypted request/response exchange over opaque broker relay
// - Device revocation teardown and rejection
func TestBroker_CrossComponentEndToEndSecurity(t *testing.T) {
	b := NewBroker()
	machineID := "mach-cross-01"
	deviceID := "dev-cross-phone-01"
	sessionID := "sess-cross-101"
	hostToken := "host-token-cross-32b"
	clientToken := "client-token-cross-32b"
	masterSecret := "devorbit-cross-master-secret-32b!"

	b.AuthorizeHost(machineID, hostToken)
	b.AuthorizeDevice(machineID, deviceID, sessionID, clientToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", b.HandleHostWebSocket)
	mux.HandleFunc("/ws/client", b.HandleClientWebSocket)

	server := httptest.NewServer(mux)
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	// Step 1: Prove query-token connection attempt is rejected
	badQueryURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s&token=%s", wsURL, machineID, deviceID, sessionID, clientToken)
	_, res, err := websocket.DefaultDialer.Dial(badQueryURL, nil)
	if err == nil || res == nil || res.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP 400 Bad Request for query-token connection attempt, got: %v", res)
	}

	// Step 2: Connect Host and Client with Header Authentication (NO token in query parameters)
	cleanHostURL := fmt.Sprintf("%s/ws/host?machine_id=%s&session_id=%s", wsURL, machineID, sessionID)
	hostHeaders := http.Header{"Authorization": []string{"Bearer " + hostToken}}
	hostConn, res, err := websocket.DefaultDialer.Dial(cleanHostURL, hostHeaders)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Host connection failed: %v", err)
	}
	defer hostConn.Close()

	cleanClientURL := fmt.Sprintf("%s/ws/client?machine_id=%s&device_id=%s&session_id=%s", wsURL, machineID, deviceID, sessionID)
	clientHeaders := http.Header{"Authorization": []string{"Bearer " + clientToken}}
	clientConn, res, err := websocket.DefaultDialer.Dial(cleanClientURL, clientHeaders)
	if err != nil || res.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Client connection failed: %v", err)
	}
	defer clientConn.Close()

	// Step 3: Derive directional E2E keys
	cKey, hKey, err := deriveTestKeys(masterSecret, sessionID, deviceID)
	if err != nil {
		t.Fatalf("Failed to derive keys: %v", err)
	}

	// Step 4: Client encrypts an application request
	seqNum := int64(1)
	timestamp := time.Now().UnixMicro()
	aadReq := constructTestAAD(sessionID, deviceID, seqNum, timestamp)
	reqPlain := []byte(`{"v":"1.2","request_id":"req-001","method":"GET","path":"/api/v1/health"}`)
	nonceReq := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

	encryptedReq := encryptTestPayload(cKey, nonceReq, reqPlain, aadReq)

	clientFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   encryptedReq,
		SeqNum:          seqNum,
		Timestamp:       timestamp,
	}

	if err := clientConn.WriteJSON(&clientFrame); err != nil {
		t.Fatalf("Failed to send frame from client: %v", err)
	}

	// Step 5: Host receives relayed frame from broker and decrypts it
	var hostReceivedFrame FrameEnvelope
	if err := hostConn.ReadJSON(&hostReceivedFrame); err != nil {
		t.Fatalf("Host failed to read frame: %v", err)
	}

	hostAAD := constructTestAAD(sessionID, deviceID, hostReceivedFrame.SeqNum, hostReceivedFrame.Timestamp)
	decryptedReq, err := decryptTestPayload(cKey, hostReceivedFrame.EncryptedData, hostAAD)
	if err != nil {
		t.Fatalf("Host failed to decrypt relayed payload: %v", err)
	}

	var parsedReq map[string]any
	if err := json.Unmarshal(decryptedReq, &parsedReq); err != nil || parsedReq["path"] != "/api/v1/health" {
		t.Fatalf("Parsed request mismatch: %v", err)
	}

	// Step 6: Host produces application response, encrypts with HostToClientKey, and sends via broker
	respPlain := []byte(`{"v":"1.2","request_id":"req-001","status_code":200,"body":"{\"status\":\"healthy\",\"os\":\"linux\"}"}`)
	respTimestamp := time.Now().UnixMicro()
	aadResp := constructTestAAD(sessionID, deviceID, seqNum, respTimestamp)
	nonceResp := []byte{11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22}

	encryptedResp := encryptTestPayload(hKey, nonceResp, respPlain, aadResp)
	hostRespFrame := FrameEnvelope{
		ProtocolVersion: "1.2",
		SessionID:       sessionID,
		Type:            "data",
		EncryptedData:   encryptedResp,
		SeqNum:          seqNum,
		Timestamp:       respTimestamp,
	}

	if err := hostConn.WriteJSON(&hostRespFrame); err != nil {
		t.Fatalf("Host failed to send response frame: %v", err)
	}

	// Step 7: Client receives relayed response frame from broker and decrypts with HostToClientKey
	var clientReceivedFrame FrameEnvelope
	if err := clientConn.ReadJSON(&clientReceivedFrame); err != nil {
		t.Fatalf("Client failed to read response frame: %v", err)
	}

	clientAAD := constructTestAAD(sessionID, deviceID, clientReceivedFrame.SeqNum, clientReceivedFrame.Timestamp)
	decryptedResp, err := decryptTestPayload(hKey, clientReceivedFrame.EncryptedData, clientAAD)
	if err != nil {
		t.Fatalf("Client failed to decrypt relayed response: %v", err)
	}

	var parsedResp map[string]any
	if err := json.Unmarshal(decryptedResp, &parsedResp); err != nil || parsedResp["status_code"] != float64(200) {
		t.Fatalf("Parsed response mismatch: %v, raw: %s", err, string(decryptedResp))
	}

	// Step 8: Device Revocation Teardown
	b.RevokeDevice(machineID, deviceID)
	time.Sleep(50 * time.Millisecond)

	if b.ActiveSessionCount() != 0 {
		t.Errorf("Expected active sessions count to be 0 after revocation")
	}

	// Subsequent connect attempt with revoked device must fail with 403 Forbidden
	_, res, err = websocket.DefaultDialer.Dial(cleanClientURL, clientHeaders)
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for revoked device, got: %v", res)
	}
}
