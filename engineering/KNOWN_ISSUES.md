# DevOrbit — Known Issues, Risks & Limitations

## 1. Security & Transport Limitations

- **Development-mode LAN HTTP pairing:** When `DEVORBIT_ENV` is not `production` and native TLS is not configured, local LAN pairing (`http://<lan-ip>:8082/api/v1/pairing/confirm`) transmits the JSON response (`session_token`, `session_secret`) over plaintext LAN HTTP. Production mode blocks this (`403`) and requires TLS/HTTPS.
- **`device_public_key` unused for response encryption:** `device_public_key` is stored/accepted for wire compatibility (`control-plane` and `openapi.yaml`), not used for key agreement or response encryption on `host-bridge`.

## 2. Environment & Validation Gaps

- **Live physical-device & reverse-proxy validation pending:** Master Plan 24 (`7ba545da10f4ec61bf180c70c60960a3af03e5d4`) has been verified with automated unit/integration tests (`httptest.NewTLSServer`, `httptest.NewServer`, Flutter `MockClient`), not yet against a physical Android/iOS device paired to a Linux/Windows host behind a live TLS reverse proxy.
- **Control-plane pairing package divergence:** `services/control-plane/internal/pairing/pairing.go` still uses its own `ConfirmPairing(challengeID, deviceID, deviceName, pubKey, proofSig)` implementation separate from `agents/host-bridge/internal/security/pairing.go` (`v1.1` timestamp/nonce proof). Both packages currently pass their own test suites; align only if control-plane-mediated pairing is activated.
- **Windows CRLF/LF warnings:** Committing from Windows produces `LF will be replaced by CRLF` warnings on files checked out with LF; keep `.gitattributes` untouched unless explicitly planned.
