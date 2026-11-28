# DevOrbit — Active Plan

## Completed Plans

### Master Plan 0 / 0.1 — Persistent Engineering Memory & Cross-Session Continuity

- **Status:** `COMPLETED` (`COMMITTED`, `PUSHED`, `REMOTE-VERIFIED`: MP0 at `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`, MP0.1 reconciled at `be4ec8aa93cdfb8463f393f9f5d1ee0d457ef60d`).

### Master Plan 24 — Pairing Bootstrap, Host-Admin Confidentiality & Closure Remediation

- **Original Starting Baseline:** `efee2c99e16427223c803b89460dbe61fd151064`
- **Initial MP24 Implementation Commit:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Closure Commit:** `2425cc838c64e7e1e28c67e33c1636d45e5889d7` (`COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` on both engineering branches)
- **Automated Stage:** `IMPLEMENTED`, `TESTED`, `REVIEWED` for Criteria A–H.
- **Outstanding Criterion:** **I — `BLOCKED`** (live TLS reverse proxy + physical Android/iOS validation).

| Criterion | Stage Status | Evidence / Notes |
|---|---|---|
| A. Host-admin routes require confidential transport in prod; ignore untrusted/ambiguous `X-Forwarded-Proto` | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `auth_middleware.go`, `pairing_transport_security_test.go` |
| B. Pairing confirmation requires confidential transport in prod | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing_handlers.go`, `pairing_transport_security_test.go` |
| C. Pairing replay & retry (`v1.1` timestamp+nonce proof; activation/revocation invalidation) | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing.go`, `pairing_test.go`, `pairing_integration_test.dart` |
| D. Direct E2E HTTP uses HKDF-derived HMAC outer auth without raw bearer tokens | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `auth_middleware.go`, `crypto_test.go`, `e2e_session_test.dart` |
| E. Production listener requires native TLS or trusted proxy, loopback-only bind, cross-platform build | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `config.go`, `console_windows.go`/`console_other.go` |
| F. Empty/restricted permissions preserved across persistence; missing/null permissions rejected fail-closed | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `session_store.go`, `session_store_test.go` |
| G. OS-backed secret protection + atomic persistence + revocation tombstones + error propagation | `IMPLEMENTED`, `TESTED`, `REVIEWED` | extended further by MP24.1 (see below) |
| H. Mobile HTTPS pairing never downgrades or falls back to HTTP | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing_client.dart`, `pairing_integration_test.dart` |
| I. Live TLS reverse-proxy (Caddy/Nginx) & physical Android/iOS validation | `BLOCKED` / `PENDING` | Missing `caddy`/`nginx`, trusted cert, and attached physical device (`adb devices -l` empty) |

## Active Master Plan

### Master Plan 24.1 — Revocation Durability & Rollback Failure Closure

- **Verified Starting HEAD:** `2425cc838c64e7e1e28c67e33c1636d45e5889d7`
- **Code + Test Commit:** `76ca2286523b1978acec1e64723d74ec64cddf2d`
- **Objective:** Close only the remaining session-revocation, rollback, Windows secret-deletion, and broker-revocation error paths found in independent review of `2425cc8`. No re-implementation of the MP24 fixes and no repository-wide re-audit.
- **Overall Status:** `IMPLEMENTED`, `TESTED`, `REVIEWED`, `COMMITTED` (push + remote verification recorded in `EXECUTION_LOG.md`).

#### Acceptance Criteria

| Criterion | Stage Status | Evidence |
|---|---|---|
| 1. Durable, idempotent revocation tombstone when the session is absent from memory; no success without persisted tombstone | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `session_store.go` (`RevokeSession`), `session_store_test.go` (`TestSessionStore_AbsentSessionRevocationTombstoneAndRollback`) |
| 1b. Persisted-only entries survive revocation of unloaded sessions; sealed secrets of unloaded sessions are destroyed | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `session_store.go` (`saveLocked` merge, `persistedSecretRefsLocked`), `TestSessionStore_AbsentRevocationDestroysPersistedSealedSecret`, `TestSessionStore_RevocationPreservesUnloadedPersistedSessions` |
| 2. Windows DPAPI supports real per-session secret deletion with fail-closed semantics | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `secret_store_windows.go`, `secret_store_windows_test.go` (3 native Windows tests) |
| 3. Rollback paths never discard `RemoveSession`/revocation errors and never return pairing success after partial provisioning | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `pairing_handlers.go`, `pairing_transport_security_test.go` |
| 4. Broker revocation errors propagate end-to-end; non-success HTTP status on required-stage failure; session-only vs device-wide semantics verified | `IMPLEMENTED`, `TESTED`, `REVIEWED` | `broker_client.go`, `broker_authorizer.go`, `handlers.go`, `cmd/host-bridge/main.go`, `services/connection-broker/internal/broker/broker.go`, `broker_test.go` |
| 5. Continuity documents corrected (stale "pending commit/push" claim removed) | `IMPLEMENTED` | `CURRENT_STATE.md`, `ACTIVE_PLAN.md`, `KNOWN_ISSUES.md`, `EXECUTION_LOG.md`, `handoffs/latest-handoff.md` |
| 6. Verification (go vet/test, Windows DPAPI tests, cross-builds, Flutter) | `IMPLEMENTED`, `TESTED` | `CURRENT_STATE.md` §3 — `-race` `BLOCKED` on this host |
| 7. Commit/push to both engineering branches with matching remote SHAs | `COMMITTED` → see `EXECUTION_LOG.md` | `main` untouched (`c49804547aef7dc1100b8b444ee32695f353e7e6`) |

## Exact Next Engineering Action

1. Push `security/atomic-agent-task-execution` and `quality/autonomous-15min-improvement`, then verify both remote heads equal the local `HEAD` (recorded in `EXECUTION_LOG.md`).
2. Run `go test -race ./internal/security/...` (Host Bridge) in Linux CI or a 64-bit CGO environment, since the Windows MinGW toolchain here cannot compile `-race`.
3. In an environment with Caddy/Nginx, a CA-trusted certificate, and a physical Android/iOS device, execute Phase 4 live validation to close Master Plan 24 Criterion I.
