# DevOrbit — Known Issues, Risks & Limitations

## 1. Security & Persistence Risks Requiring Review

- **Session-store empty-permission restoration & plaintext/revocation persistence (`agents/host-bridge/internal/security/session_store.go`):**
  - `LoadPersistedSessions` (lines 195–224, specifically lines 218–221): When `len(e.Permissions) == 0` in `~/.devorbit/host_http_sessions.json`, restoration defaults to `NewFullSessionPermissions()` instead of failing closed or preserving an explicit empty-permission map.
  - `saveLocked` / `PersistedSessionEntry` (lines 116–123, 166–193): Persists `MasterSecret` (`session_secret`) in plaintext JSON on disk (`0600`) and does not persist `Revoked` / `revokedDevices` state across Host Bridge restarts.
- **Mobile pairing HTTPS-to-HTTP fallback behavior (`apps/mobile/lib/core/pairing/pairing_client.dart`):**
  - `confirmPairing` (lines 103–109): `fallbackUris` constructs `http://localhost:$port/api/v1/pairing/confirm` and `http://10.0.2.2:$port/api/v1/pairing/confirm` with hardcoded `http://` even when `confirmUri.scheme` is `https`. Requires independent review and regression testing to verify intended scheme preservation / downgrade behavior on `SocketException`.

## 2. Transport & Protocol Limitations

- **Development-mode LAN HTTP pairing:** When `DEVORBIT_ENV` is not `production` and native TLS is not configured, local LAN pairing (`http://<lan-ip>:8082/api/v1/pairing/confirm`) transmits the JSON response (`session_token`, `session_secret`) over plaintext LAN HTTP. Production mode blocks this (`403`) and requires TLS/HTTPS.
- **`device_public_key` unused for response encryption:** `device_public_key` is stored/accepted for wire compatibility (`services/control-plane` and `packages/api-contracts/openapi.yaml`), not used for key agreement or response encryption on `agents/host-bridge`.
- **Control-plane pairing package divergence:** `services/control-plane/internal/pairing/pairing.go` still uses its own `ConfirmPairing(challengeID, deviceID, deviceName, pubKey, proofSig)` implementation separate from `agents/host-bridge/internal/security/pairing.go` (`v1.1` timestamp/nonce proof). Both pass their own test suites; align only if control-plane-mediated pairing is activated.

## 3. Environment & Validation Gaps

- **Live physical-device & reverse-proxy validation pending:** Master Plan 24 (`7ba545da10f4ec61bf180c70c60960a3af03e5d4`) has been verified with automated unit/integration tests (`httptest.NewTLSServer`, `httptest.NewServer`, Flutter `MockClient`), not yet against a physical Android/iOS device paired to a Linux/Windows host behind a live TLS reverse proxy.
- **Windows CRLF/LF warnings:** Committing from Windows produces `LF will be replaced by CRLF` warnings on files checked out with LF; keep `.gitattributes` untouched unless explicitly planned.
