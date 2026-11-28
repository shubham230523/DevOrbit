# DevOrbit — Active Plan

## Completed Plans

### Master Plan 0 / 0.1 — Persistent Engineering Memory & Cross-Session Continuity

- **Status:** `COMPLETED` (`COMMITTED`, `PUSHED`, `REMOTE-VERIFIED`: MP0 at `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`, MP0.1 reconciled at `be4ec8aa93cdfb8463f393f9f5d1ee0d457ef60d`).

## Active Master Plan

### Master Plan 24 — Pairing Bootstrap, Host-Admin Confidentiality & Closure Remediation

- **Original Starting Baseline:** `efee2c99e16427223c803b89460dbe61fd151064`
- **Initial MP24 Implementation Commit:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Closure Baseline (MP0.1):** `be4ec8aa93cdfb8463f393f9f5d1ee0d457ef60d`
- **Objective:** Close pairing bootstrap, host-admin transport confidentiality, pairing replay/retry, raw bearer on LAN E2E, production listener binding, session-store permission/secret persistence, and mobile HTTPS-to-HTTP fallback weaknesses, followed by live TLS/mobile production validation.
- **Overall Status:** `IMPLEMENTED`, `TESTED`, `REVIEWED` (independent security review & targeted remediation complete with all automated checks passing); **`PRODUCTION-VALIDATED` remains `BLOCKED` / `PENDING`** awaiting a reverse-proxy (Caddy/Nginx), trusted certificate, and physical Android/iOS device.

#### Acceptance Criteria

| Criterion | Stage Status | Evidence / Notes |
|---|---|---|
| A. Host-admin routes require confidential transport in prod; ignore untrusted/ambiguous `X-Forwarded-Proto` | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `auth_middleware.go`, `pairing_transport_security_test.go` (including multi-valued header check) |
| B. Pairing confirmation requires confidential transport in prod; `DevicePublicKey` unused claim removed | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing_handlers.go`, `pairing_transport_security_test.go` |
| C. Pairing replay & retry (`v1.1` timestamp+nonce proof; activation/revocation invalidation) | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing.go`, `pairing_test.go`, `pairing_integration_test.dart` |
| D. Direct E2E HTTP (`/api/v1/e2e`) uses HKDF-derived HMAC outer auth without raw bearer tokens | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `auth_middleware.go`, `direct_e2e_http_client.dart`, Go+Dart interop vector in `crypto_test.go` & `e2e_session_test.dart` |
| E. Production listener requires native TLS or trusted proxy with loopback-only bind & cross-platform build | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `config.go`, `console_windows.go`/`console_other.go`, `linux/amd64` + `windows/amd64` builds pass |
| F. Empty/restricted permissions preserved across persistence; missing/null permissions rejected fail-closed | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `session_store.go`, `session_store_test.go` |
| G. Session secrets protected by OS store (DPAPI / Secret Service) or fail-closed memory-only; atomic persistence + revocation tombstones + error propagation | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `secret_store*.go`, `session_store.go`, `broker_client.go`, `handlers.go`, `session_store_test.go`, `broker_client_test.go` |
| H. Mobile HTTPS pairing never downgrades or falls back to HTTP | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing_client.dart`, `pairing_integration_test.dart` |
| I. Live TLS reverse-proxy (Caddy/Nginx) & physical Android/iOS validation | `BLOCKED` / `PENDING` | Missing `caddy`/`nginx`, trusted cert, and attached physical device (`adb devices -l` empty on `Windows NT 10.0.26200.0`) |

## Exact Next Engineering Action

In an environment with Caddy or Nginx, a CA-trusted TLS certificate for the Host Bridge hostname, and a physical Android/iOS device on the same network, execute Phase 4 of Master Plan 24 Closure (start Host Bridge with production config behind the TLS reverse proxy, verify startup HTTPS QR, pair physical device, test E2E HTTP, test HTTPS downgrade prevention, test session/device revocation + restart persistence) to close Criterion I (`PRODUCTION-VALIDATED`).
