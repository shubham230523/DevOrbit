# DevOrbit — Active Plan

## Active Master Plans

### 1. Master Plan 0 — Persistent Engineering Memory & Cross-Session Continuity (Current Documentation Task)

- **Objective:** Establish `AGENTS.md`, `engineering/*`, and `engineering/handoffs/latest-handoff.md` so any fresh session, model, or account can recover exact project state from Git and repository docs without re-auditing the codebase.
- **Status:** `IN_PROGRESS` (writing and verifying continuity documents; zero application code modifications).

### 2. Master Plan 24 — Pairing Bootstrap & Host-Admin Transport Confidentiality (Preserved Active Engineering Track)

- **Original Starting Baseline:** `efee2c99e16427223c803b89460dbe61fd151064`
- **Implementation Commit:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Objective:** Close pairing bootstrap, host-admin transport confidentiality, pairing replay/retry, raw bearer on LAN E2E, and production listener binding weaknesses.
- **Overall Sign-off Status:** Code and automated tests are `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, and `REMOTE-VERIFIED` at `7ba545d`; **overall Master Plan 24 sign-off remains open pending independent review and live production/TLS validation** (do not mark Master Plan 24 as finally closed during Master Plan 0).

#### Master Plan 24 Acceptance Criteria

| Criterion | Stage Status | Notes |
|---|---|---|
| A. Host-admin routes (`/api/v1/pairing/challenge`, `/api/v1/sessions/revoke`) require confidential transport in production; ignore untrusted `X-Forwarded-Proto` | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Verified in `pairing_transport_security_test.go` |
| B. Pairing confirmation requires confidential transport in production; `DevicePublicKey` unused security claim resolved | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Wire-only compatibility retained and documented; TLS/HTTPS required in prod |
| C. Pairing replay & retry: captured requests rejected; same-device retry requires fresh `(timestamp, nonce, v1.1 proof)` and closes on activation/revocation | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Verified in `pairing_test.go`, `transport_test.go`, `pairing_transport_security_test.go`, `pairing_integration_test.dart` |
| D. Direct E2E HTTP (`/api/v1/e2e`) does not transmit raw session bearer token; uses HKDF-derived HMAC outer auth + AES-256-GCM inner tunnel | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Verified in `auth_security_test.go`, `ai_integration_test.go`, `direct_e2e_http_client_test.dart` |
| E. Production listener requires native TLS or trusted proxy with loopback-only bind | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` | Verified in `config_test.go` and `BuildStartupPairingQRPayload` |
| F. Independent review & live TLS/mobile-to-host smoke validation | `PENDING` | Next engineering validation step after Master Plan 0 |

## Exact Next Action

1. Finish committing and pushing Master Plan 0 continuity files on `security/atomic-agent-task-execution` and `quality/autonomous-15min-improvement`.
2. In the next engineering session, perform independent review of commit `7ba545da10f4ec61bf180c70c60960a3af03e5d4` (Master Plan 24) and run live TLS / mobile-to-host verification if target environment is available.
