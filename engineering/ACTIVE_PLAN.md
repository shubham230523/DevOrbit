# DevOrbit — Active Plan

## Completed Plan

### Master Plan 0 — Persistent Engineering Memory & Cross-Session Continuity

- **Status:** `COMPLETED` (`COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` at `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`, reconciled under Master Plan 0.1).
- **Result:** `AGENTS.md`, `engineering/*`, and `engineering/handoffs/latest-handoff.md` are established with zero application code changes.

## Active Master Plan

### Master Plan 24 — Pairing Bootstrap & Host-Admin Transport Confidentiality

- **Original Starting Baseline:** `efee2c99e16427223c803b89460dbe61fd151064`
- **Implementation Commit:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Latest Verified Remote Commit:** `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`
- **Objective:** Close pairing bootstrap, host-admin transport confidentiality, pairing replay/retry, raw bearer on LAN E2E, and production listener binding weaknesses.
- **Overall Status:** Automated implementation and test suites are `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, and `REMOTE-VERIFIED`; **independent review (`REVIEWED`) and live production/TLS validation (`PRODUCTION-VALIDATED`) remain `PENDING`**.

#### Acceptance Criteria

| Criterion | Stage Status | Notes |
|---|---|---|
| A. Host-admin routes (`/api/v1/pairing/challenge`, `/api/v1/sessions/revoke`) require confidential transport in production; ignore untrusted `X-Forwarded-Proto` | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Automated tests in `pairing_transport_security_test.go` |
| B. Pairing confirmation requires confidential transport in production; `DevicePublicKey` unused security claim resolved | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Wire-only compatibility retained and documented; TLS/HTTPS required in prod |
| C. Pairing replay & retry: captured requests rejected; same-device retry requires fresh `(timestamp, nonce, v1.1 proof)` and closes on activation/revocation | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Automated tests in `pairing_test.go`, `transport_test.go`, `pairing_transport_security_test.go`, `pairing_integration_test.dart` |
| D. Direct E2E HTTP (`/api/v1/e2e`) does not transmit raw session bearer token; uses HKDF-derived HMAC outer auth + AES-256-GCM inner tunnel | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Automated tests in `auth_security_test.go`, `ai_integration_test.go`, `direct_e2e_http_client_test.dart` |
| E. Production listener requires native TLS or trusted proxy with loopback-only bind | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Automated tests in `config_test.go` and `BuildStartupPairingQRPayload` |
| F. Independent security review (including `session_store.go` persistence/empty-permission restoration and `pairing_client.dart` HTTPS-to-HTTP fallback) and live TLS/mobile-to-host validation | `PENDING` | Required before closing Master Plan 24 |

## Exact Next Engineering Action

Perform an independent review of Master Plan 24 (`git diff efee2c99e16427223c803b89460dbe61fd151064..7ba545da10f4ec61bf180c70c60960a3af03e5d4`), specifically evaluating the items in `engineering/KNOWN_ISSUES.md` (`session_store.go` persistence/empty-permission restoration and `pairing_client.dart` HTTPS-to-HTTP fallback), and run live TLS reverse-proxy + mobile-to-host validation.
