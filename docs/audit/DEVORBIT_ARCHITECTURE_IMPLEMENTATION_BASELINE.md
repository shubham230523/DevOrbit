# DEVORBIT ARCHITECTURE, IMPLEMENTATION & SECURITY BASELINE AUDIT
**Phase 0.1A: Baseline Audit Correction & Evidence Reconciliation**

- **Assessment Date:** 2026-11-22
- **Repository:** `https://github.com/shubham230523/DevOrbit`
- **Branch:** `main`
- **Target Commit SHA:** `54672f28a9b6bdb5b12cc697f00aa8ce16b2e77c`
- **Parent Commit:** `55db4ba41dd44b82963162fb16aa475eb3f890cf`
- **Remote Tracking:** `origin/main` (clean working tree, in lockstep with remote)
- **Final Verdict:** `BASELINE NOT VERIFIED` (Production Runtime Lifecycle) / `BASELINE VERIFIED WITH CRITICAL DEFECTS & ARCHITECTURAL GAPS` (Component Implementation)

---

## 1. Executive Summary

A rigorous, source-level audit reconciliation was performed against the DevOrbit codebase at commit `54672f28a9b6bdb5b12cc697f00aa8ce16b2e77c` on branch `main`. This assessment corrects earlier audit claims, verifies the exact mathematical and syntactic constructs in the source code, traces the complete runtime lifecycle from process startup to session revocation, and assesses the gap between unit-level cryptographic compatibility and genuine production security verification.

### Key Audit Conclusions

1. **Production Runtime Lifecycle Is Broken:** Although unit-level tests pass for isolated packages, the complete production runtime lifecycle cannot function. In `agents/host-bridge/cmd/host-bridge/main.go`, `serverEngine.SetSessionProvisioner` is **never called**. Consequently, when a mobile client successfully completes QR pairing, the Host Bridge never provisions the newly authenticated session into its `BrokerClient`. The Host Bridge remains permanently pinned to a hardcoded startup session (`sess-host-init-001`, `phone-android-01`), which itself was never authorized on the broker.
2. **Connection Broker Defect Hangs All Tests & Violates Security Specification:** In commit `54672f2`, two critical regressions were introduced into `services/connection-broker/internal/broker/broker.go`:
   - `extractToken()` (line 143) falls back to `r.URL.Query().Get("token")` and accepts `Sec-WebSocket-Protocol: devorbit-token.<token>`, directly violating the requirement to reject URL query-string credentials with HTTP 400.
   - `HandleClientWebSocket()` (line 426) omits transmitting the `session_ready` acknowledgement frame to connecting clients. Because all broker unit and integration tests (`broker_test.go`, `broker_e2e_test.go`) block waiting for `session_ready`, running `go test ./...` in `services/connection-broker` hangs indefinitely until the 10-minute test timeout expires.
3. **Pairing Protocol Is Version 1.0 (Not v2):** The pairing engine (`agents/host-bridge/internal/security/pairing.go`) implements protocol version `"1.0"`. The challenge ID is a 35-character string prefixed with `qr-` followed by 32 hex characters (derived from 16 random bytes). The pairing payload and ephemeral secret are 64-character hex strings (derived from 32 random bytes). Proof authentication binds 6 colon-delimited fields: `version:challengeID:payload:hostID:deviceID:expiresAtStr`.
4. **Secure Storage Commit Signature Is Raw SHA-256 (Not HMAC):** The transaction commit signature in `apps/mobile/lib/core/security/secure_storage.dart` (lines 106–116) is an unkeyed SHA-256 digest (`sha256.convert(utf8.encode(raw)).toString()`), **not an HMAC**. It provides atomicity and partial-write detection across multiple key-value pairs during crash recovery, but does not provide cryptographic tamper-resistance against an attacker who has access to the storage container. Tamper resistance and confidentiality rely strictly on platform-level hardware security (Android Keystore / iOS Keychain).
5. **Cryptographic Compatibility Does Not Equal Security Verification:** Go (`agents/host-bridge/internal/security/crypto.go`) and Dart (`apps/mobile/lib/core/security/e2e_session.dart`) share compatible HKDF-SHA256 key derivation, AES-256-GCM encryption, and AAD construction. However, **Dart implements zero replay protection** (it lacks sequence number tracking), and **neither Go nor Dart validates timestamp freshness** on received frames. AAD binding prevents in-transit header modification, but does not prevent frame replay against the mobile client.
6. **Persistence, AI, and IDE Layers Are Non-Existent or Simulated:**
   - Database: Zero SQL drivers exist in Go; `schema.sql` is completely unused; all services run on volatile Go maps.
   - AI/ACP: `agents/acp-adapter` is a 38-line JSON-RPC stub; `agents/ai-router` is a 55-line dictionary returning string literals.
   - Mobile IDE: All editor, git, tasks, explorer, and AI screens in `apps/mobile` are hardcoded UI mockups using `Future.delayed`. No terminal or PTY stream exists.

---

## 2. Discrepancy Register (Previous Claims vs Source Reality vs Correction)

| Discrepancy ID | Item / Feature | Prior Claim / Architecture Doc Claim | Actual Source Code Reality | Status Classification | Required Correction / Resolution |
| :---: | :--- | :--- | :--- | :---: | :--- |
| **DISC-01** | **Broker URL Query Token Rejection** | Query string credentials are rejected with HTTP 400; credentials only accepted via headers. | `broker.go:143` contains `return r.URL.Query().Get("token"), nil`. Query credentials and subprotocol tokens are accepted. | **INCORRECT** | Must reject query tokens and subprotocol tokens in `extractToken()`; return `ErrQueryCredentialsRejected`. |
| **DISC-02** | **Broker Handshake Frame** | Broker sends explicit `session_ready` frame to client upon WebSocket upgrade. | Omitted from `broker.go:426` in commit `54672f2`. Client blocks forever awaiting handshake frame. | **INCORRECT** | Restore `conn.WriteJSON(&FrameEnvelope{Type: "session_ready", ...})` immediately after `RegisterClientConnection`. |
| **DISC-03** | **Pairing Protocol Version** | Protocol version is `"v2"` or `"2.0"`. | `pairing.go:99` sets `Version: "1.0"`. `qr_pairing_payload.dart:32` checks `v != '1.0' && v != '1'`. | **INCORRECT** | Correct specification to Version `"1.0"`. |
| **DISC-04** | **Challenge ID Generation & Format** | 32-byte cryptographically secure random challenge ID. | `pairing.go:77-81` reads 16 random bytes and formats `fmt.Sprintf("qr-%s", hex.EncodeToString(b))` (35 chars total). | **INCORRECT** | Record actual format: 16 random bytes encoded as 32 hex chars with `qr-` prefix (35 characters). |
| **DISC-05** | **Challenge Payload & Ephemeral Secret** | Ephemeral secret and payload length described ambiguously. | `pairing.go:83-93` reads 32 random bytes for payload (64 hex chars) and 32 random bytes for secret (64 hex chars). | **VERIFIED** | Explicitly document 32 raw bytes / 64 hex characters for both payload and secret. |
| **DISC-06** | **Mobile Storage Commit Signature** | Transaction log signed using HMAC for atomic write integrity. | `secure_storage.dart:106-116` computes `sha256.convert(utf8.encode(raw)).toString()`. Unkeyed SHA-256 hash. | **INCORRECT** | Clarify signature is unkeyed SHA-256 for atomic partial-write detection; platform Keystore/Keychain provides tamper-resistance. |
| **DISC-07** | **Host Dynamic Session Provisioning** | Dynamic pairing creates and provisions new session in `BrokerClient` seamlessly. | `main.go` never calls `serverEngine.SetSessionProvisioner()`. Dynamic sessions are never provisioned into `brokerClient`. | **INCORRECT** | Dynamic sessions are authorized on the broker but never provisioned on the host daemon in production. |
| **DISC-08** | **Host Startup Session Coexistence** | Startup session is ephemeral and cleanly superseded by paired session. | `main.go:80-82` pins `sess-host-init-001` and `phone-android-01` with `cfg.HostSecret`. It runs permanently and is never authorized in broker. | **INCORRECT** | Startup session remains active, repeatedly failing broker auth; dynamically paired sessions are ignored. |
| **DISC-09** | **Replay Protection Enforcement** | Sequence numbers and timestamps are verified for replay prevention across both clients. | Go enforces `seenSeqNums` in `crypto.go:155`. Dart has **no seqNum tracking**. **Neither Go nor Dart validates timestamp freshness**. | **PARTIALLY VERIFIED** | Replay protection is unidirectional (Go only); timestamp validation is entirely absent. |
| **DISC-10** | **Timestamp Freshness Window** | Frames older than 300 seconds are rejected by the crypto layer. | Neither `crypto.go`, `broker_client.go`, nor `e2e_session.dart` has timestamp window logic. "300" was an arbitrary test string. | **INCORRECT** | Timestamps are encoded into AAD but never checked for expiry or clock drift. |
| **DISC-11** | **Database Persistence** | PostgreSQL persistence for users, machines, and sessions. | Zero database drivers in `go.mod`. All Go services use volatile `sync.RWMutex` maps. Data lost on restart. | **VERIFIED (GAP)** | Confirm complete absence of database connectivity in application source. |
| **DISC-12** | **Mobile IDE Presentation** | Real remote development workspace for code, git, tasks, AI. | `CodeEditorScreen`, `GitWorkspaceScreen`, `ProjectExplorerScreen`, `RemoteTasksScreen`, `AiWorkspaceScreen` are UI mockups with `Future.delayed`. | **VERIFIED (GAP)** | Confirm all IDE workspace screens are simulated mockups. |

---

## 3. Mandatory Correction 1: Connection Broker Defect Analysis

### Defect 1.1: Missing `session_ready` Frame in `HandleClientWebSocket()`

- **File:** `services/connection-broker/internal/broker/broker.go`
- **Location:** Lines 420–436
- **Source Code at Baseline:**
  ```go
  sess, err := b.RegisterClientConnection(machineID, deviceID, sessionID, token, conn)
  if err != nil {
      conn.WriteJSON(map[string]string{"error": err.Error()})
      conn.Close()
      return
  }
  log.Printf("Broker: Authenticated Client WSS connected for machine=%s device=%s session=%s", machineID, deviceID, sessionID)

  defer b.CloseSession(sess.ID)

  for {
      var frame FrameEnvelope
      if err := conn.ReadJSON(&frame); err != nil {
          break
      }
      _ = b.RelayFrameFromClientToHost(sessionID, &frame)
  }
  ```
- **Evidence & Root Cause:** Commit `54672f2` deleted the frame transmission that previously executed immediately after `RegisterClientConnection`. The client is upgraded to WebSocket and enters a read loop, but the server never transmits the initial acknowledgement.
- **Expected Behavior:** Immediately after `RegisterClientConnection` and prior to entering the read loop, the broker must send:
  ```go
  ackFrame := &FrameEnvelope{
      ProtocolVersion: "1.2",
      SessionID:       sessionID,
      Type:            "session_ready",
      Timestamp:       time.Now().UnixMicro(),
  }
  if err := conn.WriteJSON(ackFrame); err != nil {
      conn.Close()
      return
  }
  ```

### Defect 1.2: Query-String and Subprotocol Token Acceptance in `extractToken()`

- **File:** `services/connection-broker/internal/broker/broker.go`
- **Location:** Lines 122–144
- **Source Code at Baseline:**
  ```go
  func extractToken(r *http.Request) (string, error) {
      if authHeader := r.Header.Get("Authorization"); authHeader != "" {
          parts := strings.SplitN(authHeader, " ", 2)
          if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
              token := strings.TrimSpace(parts[1])
              if token != "" {
                  return token, nil
              }
          }
      }

      if subprotocol := r.Header.Get("Sec-WebSocket-Protocol"); subprotocol != "" {
          protocols := strings.Split(subprotocol, ",")
          for _, p := range protocols {
              p = strings.TrimSpace(p)
              if strings.HasPrefix(p, "devorbit-token.") {
                  return strings.TrimPrefix(p, "devorbit-token."), nil
              }
          }
      }

      return r.URL.Query().Get("token"), nil
  }
  ```
- **Evidence & Root Cause:** Lines 133–144 parse tokens from `Sec-WebSocket-Protocol` and fallback to `r.URL.Query().Get("token")`. The security requirement mandated rejecting query-string credentials with HTTP 400 and rejecting subprotocol tokens with HTTP 401. Commit `54672f2` accidentally reintroduced this fallback.
- **Expected Behavior:** `extractToken()` must check if query parameters contain `token`, `credential`, or `secret`, returning `ErrQueryCredentialsRejected`. It must only accept valid `Authorization: Bearer <token>` headers.

### Defect 1.3: Impact on Test Suites

In `services/connection-broker/internal/broker`:
1. `broker_test.go:96-102`:
   ```go
   var ackFrame FrameEnvelope
   if err := clientConn.ReadJSON(&ackFrame); err != nil {
       t.Fatalf("Failed to read session_ready ack frame: %v", err)
   }
   ```
2. `broker_test.go:396-399`:
   ```go
   var ack FrameEnvelope
   if err := clientConn.ReadJSON(&ack); err != nil || ack.Type != "session_ready" {
       t.Fatalf("Expected session_ready ack frame, got: %v, err: %v", ack, err)
   }
   ```
3. `broker_test.go:471-473`: Client reads `ack` frame.
4. `broker_e2e_test.go:127-133`: Client reads `session_ready` frame.
5. `broker_e2e_test.go:246-249`: Client reads `session_ready` frame.

**Result:** When `go test ./...` runs in `services/connection-broker`, the compiled test binary `broker.test.exe` invokes these test cases sequentially. The client connection calls `ReadJSON()` on a WebSocket connection where the broker never sends any bytes. The test runner deadlocks on network I/O, consuming 0% CPU and hanging indefinitely until the process is aborted or timed out.

---

## 4. Mandatory Correction 2: Pairing Protocol Specification & Verification

### Exact Protocol Parameters from `agents/host-bridge/internal/security/pairing.go`

```
+----------------------------------------------------------------------------------------------------+
|                                    PAIRING CHALLENGE ATTRIBUTES                                    |
+----------------------+--------------------+----------------------------------+---------------------+
| Field                | JSON Key           | Source Generation Function       | Length / Format     |
+----------------------+--------------------+----------------------------------+---------------------+
| Protocol Version     | "v"                | PairingChallenge.Version         | "1.0" (exact string)|
| Challenge ID         | "challenge_id"     | "qr-" + hex(rand(16))            | 35 characters       |
| Pairing Payload      | "pairing_payload"  | hex(rand(32))                    | 64 hex characters   |
| Ephemeral Secret     | "pairing_secret"   | hex(rand(32))                    | 64 hex characters   |
| Host ID              | "host_id"          | p.hostID                         | "host-<hash/random>"|
| Host Name            | "host_name"        | p.hostName                       | Configured hostname |
| Expires At           | "expires_at"       | time.Now().Add(ttl)              | time.Time (5 min)   |
| Expires At String    | "expires_at_str"   | expiresAt.Format(time.RFC3339)   | ISO 8601 string     |
+----------------------+--------------------+----------------------------------+---------------------+
```

### Proof Construction & Verification Trace

1. **Authenticated Data String Construction:**
   - **Go Definition (`pairing.go:62-64`):**
     ```go
     func ConstructProofAuthenticatedData(version, challengeID, payload, hostID, deviceID, expiresAtStr string) string {
         return fmt.Sprintf("%s:%s:%s:%s:%s:%s", version, challengeID, payload, hostID, deviceID, expiresAtStr)
     }
     ```
   - **Dart Definition (`secure_storage.dart:268-270`):**
     ```dart
     final authData = '$version:$challengeId:$payload:$hostId:$deviceId:$expiresAtStr';
     ```
   - **Canonical String Format:**
     `1.0:qr-<32-hex-chars>:<64-hex-chars>:<host-id>:<device-id>:<rfc3339-timestamp>`
2. **Signature Calculation:**
   - **Go (`pairing.go:66-71`):**
     ```go
     mac := hmac.New(sha256.New, []byte(ephemeralSecret))
     mac.Write([]byte(authData))
     return hex.EncodeToString(mac.Sum(nil))
     ```
   - **Dart (`secure_storage.dart:271-274`):**
     ```dart
     final key = utf8.encode(secretKey);
     final bytes = utf8.encode(authData);
     final hmacSha256 = Hmac(sha256, key);
     return hmacSha256.convert(bytes).toString();
     ```
3. **Verification & Replay Prevention (`pairing.go:123-149`):**
   - Challenge existence check (`p.challenges[chID]`).
   - Single-use consumption check (`if ch.Consumed { return "", ErrPairingUsed }`).
   - Expiration check (`if time.Now().After(ch.ExpiresAt) { return "", ErrPairingExpired }`).
   - Constant-time verification: `subtle.ConstantTimeCompare([]byte(proofSig), []byte(expectedSig)) == 1`.
   - Atomic state mutation: `ch.Consumed = true` under `p.mu.Lock()`.
   - Issuance of client session token: 32 random bytes encoded as 64 hex characters.

---

## 5. Mandatory Correction 3: Mobile Secure Storage Architecture

### Unkeyed SHA-256 Commit Signature vs HMAC

- **File:** `apps/mobile/lib/core/security/secure_storage.dart`
- **Location:** Lines 106–116
- **Source Code:**
  ```dart
  static String _calculateCommitSignature({
    required String machineId,
    required String deviceId,
    required String sessionId,
    required String token,
    required String masterSecret,
    String? brokerUrl,
  }) {
    final raw = '$machineId|$deviceId|$sessionId|$token|$masterSecret|${brokerUrl ?? ""}';
    return sha256.convert(utf8.encode(raw)).toString();
  }
  ```

### Security Boundary Analysis

```
+--------------------------------------------------------------------------------+
|                         MOBILE CREDENTIAL STORAGE LAYERS                       |
+--------------------------------------------------------------------------------+
|  Application Layer: SecureStorageService                                      |
|  - Multi-key write orchestration (machineId, deviceId, sessionId, token, etc.) |
|  - Integrity Mechanism: Unkeyed SHA-256 Commit Checksum                        |
|    * PROTECTS: Detects partial/interrupted multi-key writes on app crash/abort.|
|    * DOES NOT PROTECT: Tamper resistance (attacker with disk access can forge) |
+--------------------------------------------------------------------------------+
                                    |
                                    v (Delegates individual key-value pairs)
+--------------------------------------------------------------------------------+
|  Platform Driver Layer: PlatformSecureStorageDriver (FlutterSecureStorage)    |
|  - Android: EncryptedSharedPreferences backed by Android Keystore Master Key   |
|  - iOS: Keychain Services with kSecAttrAccessibleAfterFirstUnlock              |
|    * PROTECTS: Cryptographic confidentiality, hardware-backed encryption,      |
|      OS sandboxing, platform tamper-resistance.                               |
+--------------------------------------------------------------------------------+
```

1. **What the SHA-256 Checksum Protects:**
   Because `FlutterSecureStorage` does not provide atomic transactions across multiple separate keys, writing 6 distinct credential keys can be interrupted midway (e.g., process termination, crash, battery pull). When saving a session, `SecureStorageService` deletes the commit signature, writes all 6 keys, and then writes the SHA-256 commit signature. When reading, if `commitSig != expectedSig` or any key is missing, the service fails closed and calls `clearPairedSession()`.
2. **What the SHA-256 Checksum Does NOT Protect:**
   It is unkeyed. It does not use HMAC. If an attacker gains raw write access to the storage file system, they could alter credentials and recompute `sha256(raw)`.
3. **Platform Secure Storage Role:**
   Tamper-resistance and cryptographic confidentiality are enforced strictly by the OS platform keystores (Android Keystore / iOS Keychain), not by application-layer Dart code.

---

## 6. Mandatory Correction 4: Host Startup & Session Lifecycle Trace

### Hardcoded Startup Credentials in `main.go`

- **File:** `agents/host-bridge/cmd/host-bridge/main.go`
- **Location:** Lines 79–85
- **Source Code:**
  ```go
  // 5. Outbound Connection Broker Client
  brokerClient := transport.NewBrokerClient(cfg.BrokerWSURL, cfg.HostID, "sess-host-init-001", cfg.HostSecret, serverEngine)
  e2eSess, _ := security.NewE2ESession("sess-host-init-001", "phone-android-01", cfg.HostSecret, 24*time.Hour)
  brokerClient.SetE2ESession(e2eSess)

  brokerClient.Start(context.Background())
  defer brokerClient.Stop()
  ```

### The Unwired Provisioning Gap

```mermaid
sequenceDiagram
    participant Mobile as Mobile App
    participant Handlers as Host Pairing HTTP Handlers
    participant Engine as Server Engine
    participant Client as Host BrokerClient
    participant Broker as Connection Broker

    Note over Handlers,Client: Startup Phase (cmd/host-bridge/main.go)
    Engine->>Client: NewBrokerClient(..., "sess-host-init-001", HostSecret)
    Engine->>Client: SetE2ESession("sess-host-init-001", "phone-android-01", HostSecret)
    Client->>Broker: Connects /ws/host?session_id=sess-host-init-001
    Broker-->>Client: 401 Unauthorized (session not authorized in broker)
    Note over Engine: DEFECT: SetSessionProvisioner() NEVER CALLED IN main.go!

    Note over Mobile,Broker: Dynamic Pairing Phase
    Mobile->>Handlers: POST /api/v1/pairing/confirm
    Handlers->>Broker: POST /api/v1/internal/sessions/authorize (Registers sess-random)
    Broker-->>Handlers: 200 OK (sess-random authorized)
    Handlers->>Engine: if s.sessionProvisioner != nil { ... }
    Note over Handlers: s.sessionProvisioner is NIL! Skipped!
    Handlers-->>Mobile: 200 OK {session_id: sess-random, token: clientToken}

    Note over Mobile,Broker: Connection Phase
    Mobile->>Broker: Connects /ws/client?session_id=sess-random
    Broker-->>Mobile: [BUG: session_ready omitted; client hangs]
    Note over Client: Host NEVER connects for sess-random!<br/>Host permanently stuck retrying sess-host-init-001!
```

### Lifecycle Trace Analysis

1. **Startup Session (`sess-host-init-001`):**
   - Created with hardcoded ID `"sess-host-init-001"`, device ID `"phone-android-01"`, and master secret `cfg.HostSecret`.
   - Never authorized with the broker at startup. The broker has no knowledge of `"sess-host-init-001"`, so the host's background dialer repeatedly receives HTTP 401 Unauthorized from the broker.
2. **Dynamically Paired Session Provisioning:**
   - In `agents/host-bridge/internal/transport/http/pairing_handlers.go:223-231`:
     ```go
     if s.sessionProvisioner != nil {
         if err := s.sessionProvisioner(sessionID, req.DeviceID, sessionSecret, expiry); err != nil { ... }
     }
     ```
   - In `cmd/host-bridge/main.go`, `serverEngine.SetSessionProvisioner()` is **never invoked**.
   - `s.sessionProvisioner` is `nil`.
   - When a mobile device pairs, the handler authorizes the session on the broker, but **fails to notify `BrokerClient`**.
3. **Session Coexistence / Replacement:**
   - The initial startup session is **never replaced**.
   - The dynamically paired session is **never provisioned**.
   - The host daemon never initiates a WebSocket connection for the paired mobile session.
4. **Credential Mismatch:**
   - Host `BrokerClient` is initialized with `HostToken = cfg.HostSecret`.
   - Dynamic sessions generate an ephemeral per-session `sessionSecret` (32 random bytes) that must differ from `HostSecret` (`broker_client.go:138`).
   - Because provisioning never occurs in `main.go`, the host bridge cannot decrypt frames from dynamically paired mobile devices.

---

## 7. Mandatory Correction 5: Cryptographic Compatibility vs Security Verification

### Cryptographic Primitive Comparison: Go vs Dart

| Cryptographic Attribute | Go Implementation (`agents/host-bridge`) | Dart Implementation (`apps/mobile`) | Compatibility Status |
| :--- | :--- | :--- | :---: |
| **KDF Algorithm** | HKDF with SHA-256 (`golang.org/x/crypto/hkdf`) | HKDF with HMAC-SHA256 (`package:cryptography`) | **IDENTICAL** |
| **KDF Master Secret** | Raw UTF-8 bytes of `masterSecret` | `utf8.encode(masterSecret)` | **IDENTICAL** |
| **KDF Salt** | `"devorbit-e2e-salt-" + sessionID` | `"devorbit-e2e-salt-$sessionId"` | **IDENTICAL** |
| **KDF Info (Client->Host)**| `"devorbit-client-to-host-" + sessionID + "-" + deviceID` | `"devorbit-client-to-host-$sessionId-$deviceId"` | **IDENTICAL** |
| **KDF Info (Host->Client)**| `"devorbit-host-to-client-" + sessionID + "-" + deviceID` | `"devorbit-host-to-client-$sessionId-$deviceId"` | **IDENTICAL** |
| **Derived Key Length** | 32 bytes (256-bit AES) | 32 bytes (256-bit AES) | **IDENTICAL** |
| **Cipher Algorithm** | AES-GCM 256-bit (`crypto/cipher`) | AES-GCM 256-bit (`AesGcm.with256bits()`) | **IDENTICAL** |
| **Nonce Length & Source** | 12 bytes from `crypto/rand` | 12 bytes from `Random.secure()` | **IDENTICAL** |
| **Tag Length & Wire Format**| 16 bytes appended: `nonce (12) + ciphertext + tag (16)` | 16 bytes appended: `nonce (12) + ciphertext + tag (16)` | **IDENTICAL** |
| **Wire Encoding** | Lowercase hex string (`hex.EncodeToString`) | Lowercase hex string (`toRadixString(16).padLeft(2, '0')`)| **IDENTICAL** |
| **AAD Format** | `sessionID + ":" + deviceID + ":" + uint64(seq) + uint64(ts)` | `sessionId + ":" + deviceId + ":" + uint64(seq) + uint64(ts)`| **IDENTICAL** |

### AAD Binding vs Replay Protection & Timestamp Validation

```
+------------------------------------------------------------------------------------------------+
|                        CRYPTOGRAPHIC COMPATIBILITY VS SECURITY VERIFICATION                    |
+------------------------------------------------------------------------------------------------+
| Property                  | Go Receiver (Host Bridge)          | Dart Receiver (Mobile Client) |
+---------------------------+------------------------------------+-------------------------------+
| Decryption / Tag Check    | VERIFIED (Fails if AAD altered)    | VERIFIED (Fails if AAD altered|
| Sequence Number Tracking  | VERIFIED (seenSeqNums[seqNum])     | NOT IMPLEMENTED (Ignored)     |
| Strictly Increasing Order | NOT IMPLEMENTED (Map lookup only)  | NOT IMPLEMENTED               |
| Replay Rejection          | VERIFIED (ErrReplayedFrame)        | NOT IMPLEMENTED (Vulnerable)  |
| Timestamp Freshness Check | NOT IMPLEMENTED (Never checked)    | NOT IMPLEMENTED (Never checked|
| Max Clock Skew Window     | NOT IMPLEMENTED                    | NOT IMPLEMENTED               |
+------------------------------------------------------------------------------------------------+
```

1. **AAD Binding Is Not Replay Prevention:**
   AAD (Additional Authenticated Data) binds `sessionID`, `deviceID`, `seqNum`, and `timestamp` into the AES-GCM authentication tag. If an attacker alters the sequence number or timestamp in transit, decryption fails with an authentication error. However, **AAD binding does not prevent an attacker from replaying an unmodified valid frame**.
2. **Dart Mobile Client Is Vulnerable to Replay Attacks:**
   In `apps/mobile/lib/core/security/e2e_session.dart:103-138`, `FlutterE2ESession.decrypt` takes `seqNum` and `timestamp`, constructs AAD, and calls `_aesGcm.decrypt()`. There is **no tracking of seen sequence numbers** and **no state checking**. If an eavesdropped host-to-client frame is replayed, the mobile client will accept and decrypt it as valid every single time.
3. **Timestamp Freshness Is Not Validated Anywhere:**
   Neither Go nor Dart validates that `timestamp` is recent. There is no maximum age check (the previously claimed "300-second window" does not exist in any production code). A frame captured hours earlier can be replayed to the mobile client without triggering any timestamp rejection.

---

## 8. Mandatory Correction 6: Architecture & Implementation Status Classification

| Repository Relative Path | Symbol / Function | Line Numbers | Classification | Factual Evidence & Technical Notes |
| :--- | :--- | :---: | :---: | :--- |
| `agents/host-bridge/internal/security/pairing.go` | `PairingEngine.CreateChallenge` | 73–112 | **VERIFIED** | Generates version "1.0", 35-char challenge ID (`qr-` + 32 hex), 64-hex payload, 64-hex secret, 5-min TTL. |
| `agents/host-bridge/internal/security/pairing.go` | `PairingEngine.ConfirmPairing` | 114–158 | **VERIFIED** | Enforces constant-time comparison of HMAC-SHA256 proof over 6 fields; marks consumed atomically. |
| `agents/host-bridge/internal/security/crypto.go` | `DeriveDirectionalKeys` | 44–63 | **VERIFIED** | Derives separate 32-byte client and host AES keys using HKDF-SHA256 with directional info strings. |
| `agents/host-bridge/internal/security/crypto.go` | `E2ESession.Decrypt` | 149–198 | **PARTIALLY VERIFIED** | Replay check exists via `seenSeqNums[seqNum]`; timestamp freshness check is completely absent. |
| `agents/host-bridge/internal/security/workspace_jail.go`| `WorkspaceJail.SafePath` | 38–62 | **VERIFIED** | Resolves symlinks via `filepath.EvalSymlinks`; blocks directory traversal beyond root workspace. |
| `agents/host-bridge/cmd/host-bridge/main.go` | `main` | 79–85 | **INCORRECT (LIFECYCLE)**| Hardcodes `sess-host-init-001`; never wires `serverEngine.SetSessionProvisioner`; dynamic pairing fails. |
| `agents/host-bridge/internal/transport/http/pairing_handlers.go` | `HandleConfirmPairing` | 223–231 | **PARTIALLY VERIFIED** | Correctly authorizes broker, but skips local provisioning because `s.sessionProvisioner == nil`. |
| `services/connection-broker/internal/broker/broker.go` | `extractToken` | 122–144 | **INCORRECT (SECURITY)**| Re-allows query-string token fallback (`r.URL.Query().Get("token")`) and subprotocol tokens. |
| `services/connection-broker/internal/broker/broker.go` | `HandleClientWebSocket` | 420–436 | **INCORRECT (PROTOCOL)**| Omits `session_ready` frame dispatch upon client upgrade; hangs all client WebSocket connections. |
| `apps/mobile/lib/core/security/secure_storage.dart` | `_calculateCommitSignature` | 106–116 | **VERIFIED** | Raw SHA-256 digest of 6 concatenated fields; protects multi-key write atomicity, not tamper proofing. |
| `apps/mobile/lib/core/security/e2e_session.dart` | `FlutterE2ESession.decrypt` | 103–138 | **PARTIALLY VERIFIED** | AES-256-GCM decryption works; completely lacks sequence number tracking and timestamp checks. |
| `apps/mobile/lib/core/pairing/pairing_client.dart` | `PairingClient.confirmPairing` | 41–140 | **VERIFIED** | Validates absolute confirm URI; sends HMAC-SHA256 proof over HTTP POST; parses response credentials. |
| `apps/mobile/lib/features/editor/...` | `CodeEditorScreen._saveFile` | 45–65 | **NOT VERIFIED (MOCK)** | Hardcoded Kotlin sample; simulates saving with `Future.delayed(400ms)`; no remote bridge calls. |
| `apps/mobile/lib/features/git/...` | `GitWorkspaceScreen` | 15–26 | **NOT VERIFIED (MOCK)** | Hardcoded staged/unstaged lists and static diff string; no communication with host Git engine. |
| `apps/mobile/lib/features/tasks/...` | `RemoteTasksScreen._runTask` | 25–48 | **NOT VERIFIED (MOCK)** | Fake Gradle build logs streamed via `Future.delayed(500ms)`; no remote process execution. |
| `apps/mobile/lib/features/ai_workspace/...` | `AiWorkspaceScreen._sendPrompt`| 33–48 | **NOT VERIFIED (MOCK)** | Returns canned assistant response with static build.gradle.kts diff; no ACP/AI router connection. |
| `agents/acp-adapter/internal/acp/acp.go` | `ACPAdapter.ParseMessage` | 23–37 | **PARTIALLY VERIFIED** | Unmarshals JSON-RPC 2.0 string; contains no agent execution, MCP tools, or stdio/SSE transports. |
| `agents/ai-router/internal/router/router.go` | `AIRouter.RoutePrompt` | 38–54 | **PARTIALLY VERIFIED** | Dictionary mapping 5 model names to provider strings; zero network calls or LLM SDK integrations. |
| `infrastructure/database/schema.sql` | `schema.sql` | 1–45 | **NOT VERIFIED (UNUSED)**| PostgreSQL DDL schema present in repo, but zero Go services contain database drivers or connect to DB. |

---

## 9. Comprehensive 18-Stage Lifecycle Verification Trace

```
+--------------------------------------------------------------------------------------------------------------------+
|                                      DEVORBIT 18-STAGE CONNECTION LIFECYCLE                                        |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| #  | Stage Name                         | Primary Source Location  | Implementation Status | Technical Reality     |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 1  | Host Startup                       | host-bridge/main.go:24   | IMPLEMENTED           | Loads config; checks  |
|    |                                    |                          |                       | env & TLS in prod.    |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 2  | Host Identity Creation             | host-bridge/config.go:120| IMPLEMENTED           | SHA-256 machine ID    |
|    |                                    |                          |                       | from hostname + MACs. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 3  | Mobile Identity Creation           | secure_storage.dart:86   | IMPLEMENTED           | UUIDv4 generated &    |
|    |                                    |                          |                       | saved in Keystore.    |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 4  | Pairing Challenge Creation         | pairing.go:73            | IMPLEMENTED           | v1.0, 35-char ID,     |
|    |                                    |                          |                       | 64-hex payload/secret.|
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 5  | Pairing Proof Generation           | pairing_client.dart:52   | IMPLEMENTED           | HMAC-SHA256 over 6    |
|    |                                    |                          |                       | canonical fields.     |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 6  | Pairing Proof Verification         | pairing.go:114           | IMPLEMENTED           | Constant-time compare;|
|    |                                    |                          |                       | atomic single-use.    |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 7  | Broker Device Authorization        | broker_authorizer.go:35  | IMPLEMENTED           | REST call to broker;  |
|    |                                    |                          |                       | registers tokens/exp. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 8  | Credential Issuance                | pairing_handlers.go:247  | IMPLEMENTED           | Returns session ID,   |
|    |                                    |                          |                       | client token, expiry. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 9  | Host Broker Authentication         | broker_client.go:275     | DEFECTIVE IN RUNTIME  | Tries hardcoded init  |
|    |                                    |                          |                       | session; gets 401.    |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 10 | Mobile Broker Authentication       | broker_ws_client.dart:65 | IMPLEMENTED           | Connects with Bearer  |
|    |                                    |                          |                       | Authorization header. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 11 | Session Handshake                  | broker.go:426            | BROKEN (REGRESSION)   | Broker omits          |
|    |                                    |                          |                       | session_ready frame!  |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 12 | Message Routing                    | broker.go:439, 467       | IMPLEMENTED (LOGIC)   | Opaque relay between  |
|    |                                    |                          |                       | client and host conns.|
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 13 | E2E Encryption / Decryption        | crypto.go / e2e_sess.dart| PARTIALLY VERIFIED    | Crypto works; mobile  |
|    |                                    |                          |                       | lacks replay check.   |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 14 | Session Expiration                 | broker.go:448, 477       | IMPLEMENTED           | Bounded TTL enforced; |
|    |                                    |                          |                       | closes on expiration. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 15 | Reconnection                       | broker_client.go:300     | IMPLEMENTED           | Backoff loop preserves|
|    |                                    |                          |                       | valid tokens/expiry.  |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 16 | Device Revocation                  | broker.go:680            | IMPLEMENTED           | Drops active sockets; |
|    |                                    |                          |                       | blocks reconnect.     |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 17 | Host Disconnection                 | broker.go:540            | IMPLEMENTED           | Closing host socket   |
|    |                                    |                          |                       | severs client socket. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
| 18 | Session Cleanup                    | crypto.go:87             | IMPLEMENTED           | Keys zeroed in RAM;   |
|    |                                    |                          |                       | storage keys deleted. |
+----+------------------------------------+--------------------------+-----------------------+-----------------------+
```

---

## 10. Test Execution Evidence Table

All tests were executed uncached (`-count=1`) directly against the local checkout at HEAD `54672f28a9b6bdb5b12cc697f00aa8ce16b2e77c`:

| Test Suite / Target | Exact Command Line Executed | Tests Passed | Tests Failed | Execution Duration | Status Verdict | Observed Behavior & Failure Analysis |
| :--- | :--- | :---: | :---: | :---: | :---: | :--- |
| `services/control-plane` | `cd services/control-plane; go test -v -count=1 ./...` | 5 | 0 | 2.273s | **PASS** | Token lifecycle and pairing challenge tests pass cleanly. |
| `agents/host-bridge/internal/config` | `cd agents/host-bridge; go test -v -count=1 ./internal/config` | 6 | 0 | 1.070s | **PASS** | Environment validation and config security checks pass. |
| `agents/host-bridge/internal/discovery`| `cd agents/host-bridge; go test -v -count=1 ./internal/discovery` | 1 | 0 | 0.959s | **PASS** | Android workspace project scanner passes. |
| `agents/host-bridge/internal/sanitizer`| `cd agents/host-bridge; go test -v -count=1 ./internal/sanitizer` | 2 | 0 | 0.981s | **PASS** | Path sanitizer and path traversal checks pass. |
| `agents/host-bridge/internal/security` | `cd agents/host-bridge; go test -v -count=1 ./internal/security` | 11 | 0 | 1.046s | **PASS** | HKDF derivation, AAD tamper rejection, and jail pass. |
| `agents/host-bridge/internal/infra/...`| `cd agents/host-bridge; go test -v -count=1 ./internal/infrastructure/...` | 4 | 0 | 3.725s | **PASS** | ADB, filesystem atomic write, Git diff, and process pass.|
| `agents/host-bridge/internal/transport`| `cd agents/host-bridge; go test -v -count=1 ./internal/transport/...` | 8 | 0 | 6.254s | **PASS** | BrokerClient, HTTP API, and trusted proxy tests pass. |
| `agents/host-bridge/tests/e2e` | `cd agents/host-bridge; go test -v -count=1 ./tests/e2e/...` | 2 | 0 | 2.898s | **PASS** | End-to-end dev workflow and pairing tests pass. |
| `agents/acp-adapter` | `cd agents/acp-adapter; go test -v -count=1 ./...` | 2 | 0 | 0.850s | **PASS** | JSON-RPC 2.0 unmarshaling tests pass. |
| `agents/ai-router` | `cd agents/ai-router; go test -v -count=1 ./...` | 2 | 0 | 0.776s | **PASS** | Model registration and routing dictionary tests pass. |
| `apps/mobile` (`flutter test`) | `cd apps/mobile; flutter test` | 23 | 0 | 6.000s | **PASS** | Secure storage, QR dialog, E2E session crypto pass. |
| `apps/mobile` (`flutter analyze`) | `cd apps/mobile; flutter analyze` | 0 issues | 0 | 35.200s | **PASS** | Zero analyzer errors or lint warnings. |
| `services/connection-broker` | `cd services/connection-broker; go test -v -count=1 ./...` | 0 | 0 | >10m00s | **HANG / TIMEOUT** | Deadlocks at `clientConn.ReadJSON(&ackFrame)` due to missing `session_ready` frame in `broker.go:426`. |

---

## 11. Final Baseline Audit Verdict

# `BASELINE NOT VERIFIED` (Production Runtime Lifecycle)
### `BASELINE VERIFIED WITH CRITICAL DEFECTS & ARCHITECTURAL GAPS` (Component Level)

### Justification
1. **Production Runtime Lifecycle Cannot Function:** The production startup path in `agents/host-bridge/cmd/host-bridge/main.go` fails to wire `serverEngine.SetSessionProvisioner()`. When pairing occurs, the newly authorized session is never provisioned on the host's `BrokerClient`. The host daemon remains locked trying to authenticate a hardcoded, unauthorized session (`sess-host-init-001`), completely severing the mobile client from the host bridge.
2. **Connection Broker Test Suite Is Broken:** Commit `54672f2` broke the Connection Broker's WebSocket handshake by dropping the `session_ready` frame transmission and reintroduced query-token acceptance. As a result, `services/connection-broker` cannot pass its own test suite and hangs indefinitely.
3. **Absence of Real Application Backing:** The database persistence layer is purely decorative DDL with no Go drivers; the AI components are superficial stubs; and all mobile IDE screens are simulated mockups.

---

### Audit Verification Attestation
This audit correction was performed strictly via read-only source code inspection, static analysis, and execution of existing test suites. No production source code, configuration files, dependencies, database migrations, or git tracking configurations were altered. Only this audit documentation file was updated.
