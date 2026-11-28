# DevOrbit — Current State

**Last Updated:** 2026-11-28
**Current Branch:** `security/atomic-agent-task-execution`
**Verified Starting HEAD (MP25 M1):** `6e93ea43d815182bddcc6497df90c16f8aebcbda`
**Milestone SHAs:** MP24 closure `2425cc8`, MP24.1 `76ca228` (+ docs `d711662`, `6e93ea4`), MP25 assessment read-only.
**Baseline for this file's verification runs:** `6e93ea4` + uncommitted M1 work (see `EXECUTION_LOG.md` Entry 8).

## 1. Stage Status Matrix

| Workstream | Status | Notes |
|---|---|---|
| Master Plan 23 (Host Bridge HTTP Auth & Trust Boundary) | `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED` | `efee2c9` |
| Master Plan 0 / 0.1 (Continuity Infrastructure) | `COMPLETED` | `7aed096` / `be4ec8a` |
| Master Plan 24 Criteria A–H | `IMPLEMENTED`, `TESTED`, `REVIEWED`, `COMMITTED`, `PUSHED` | `2425cc8` |
| MP24 Criterion I (live TLS + physical devices) | `BLOCKED` | procedure written; execution needs DNS/cert/proxy host/devices |
| Master Plan 24.1 (revocation durability, DPAPI deletion, rollback errors) | `IMPLEMENTED`, `TESTED`, `REVIEWED`, `COMMITTED`, `PUSHED` | `76ca228` |
| Master Plan 25 assessment | `COMPLETED` (read-only) | `DEVORBIT_PRODUCTION_READINESS_MASTER_ASSESSMENT.md` |
| **Master Plan 25 · M1** | **`IMPLEMENTED`, `TESTED` (repo) / `BLOCKED` (external evidence)** | Verdict: **M1 PARTIALLY COMPLETE** |

## 2. M1 Deliverables (verified in this session)

### 2.1 Connectivity truthfulness (G-12)

- `apps/mobile/lib/core/network/connection_manager.dart` — rewritten from a *simulated* state machine
  (artificial delay + debug-only heartbeat, zero production usages) into a real supervisor over
  `BrokerWebSocketClient`:
  - `isTransportAuthenticated` is true only after a broker `session_ready`/`auth_ack`.
  - Revoked/expired are terminal; `connect()` refuses until `resetAfterRePairing()`.
  - Duplicate connect attempts are refused (no duplicate sockets).
  - Generation counter discards events from superseded sockets.
  - Bounded exponential backoff with jitter (`computeBackoffDelay`), plus `reconnect(brokerUrl:, mode:)`
    for Wi-Fi ↔ mobile-network transitions.
- `broker_websocket_client.dart` — added `autoReconnect`/backoff/jitter/Random injection, generation
  guards on every async callback, in-flight connect guard, sticky terminal states, config-error vs
  auth-error classification, and `connectionFailed` surfacing on unexpected closure when
  self-reconnect is disabled.
- `dashboard_screen.dart` — all connection status now flows from `ConnectionManager`; requests are
  blocked unless the transport is authenticated.

### 2.2 Android release integrity (G-H3/G-28)

- Signing credentials resolved from env vars (`DEVORBIT_ANDROID_*`) or a git-ignored
  `key.properties`; **release builds fail closed** when absent (verified).
- `android:label="DevOrbit"`, `allowBackup=false`, backup/data-extraction rules exclude secure
  storage, `networkSecurityConfig` (release cleartext **disabled**, debug source set scoped for LAN),
  camera feature optional, `minSdk ≥ 24`.
- Documentation: `apps/mobile/android/KEYSTORE_SETUP.md`, `key.properties.example`, `proguard-rules.pro`
  (R8 rules prepared; minify intentionally off until device validation).

### 2.3 iOS store-critical config (G-29)

- `NSLocalNetworkUsageDescription` added; ATS `NSAllowsLocalNetworking=true` only (no
  `NSAllowsArbitraryLoads`); expanded camera rationale; no background modes claimed.
- `apps/mobile/ios/IOS_STORE_READINESS.md` documents prerequisites and the blocked compile state.

### 2.4 CI (G-27)

`.github/workflows/ci.yml` now runs: `go vet` + `go test` matrix over 5 modules; **`go test -race`
with `CGO_ENABLED=1` on ubuntu-latest**; linux/windows cross-builds; `flutter analyze`/`flutter test`;
Android debug build + artifact; signed release build gated to `push` + secret presence with
`apksigner` debug-key rejection; gitleaks; `govulncheck`. Least-privilege `permissions`, concurrency
cancellation, triggers for `main`, `security/**`, `quality/**`, `feature/**`.

### 2.5 TLS deployment prerequisites (G-10)

`infrastructure/deployment/Caddyfile` (TLS/HSTS, `wss` upgrade, health checks, single
`X-Forwarded-Proto`), `env.example`, rewritten `deployment/README.md`, and
`docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md` (executable, evidence table PENDING).

## 3. Verified Verification Results (this session, after the final edit)

| Command | Result |
|---|---|
| `go vet ./...` + `go test -count=1 ./...` — all 5 Go modules | **PASS** (exit 0) |
| `GOOS=linux`/`windows` `go build ./...` — host-bridge, connection-broker | **PASS** |
| `flutter analyze` | **PASS** — `No issues found!` |
| `flutter test` | **PASS** — `+49: All tests passed!` (37 existing + 12 new) |
| `flutter build apk --debug` | **PASS** — `app-debug.apk` built |
| `flutter build apk --release` (no signing creds) | **PASS (expected refusal)** — `DevOrbit release build refused: no release signing credentials configured.` |
| `flutter build apk --release` (ephemeral temp keystore, deleted) | **PASS** — `app-release.apk (14.5MB)` |
| `go test -race` (this Windows host) | **BLOCKED** — 32-bit-only MinGW (`cc1.exe: 64-bit mode not compiled in`); CI job added |
| iOS compile / TestFlight | **BLOCKED** — no macOS/Xcode |
| Live TLS + physical devices | **BLOCKED** — no DNS/cert/proxy host/devices |

## 4. Environment defect (not a code defect)

Gradle aborts with
`AndroidLocationsException: Several environment variables … contain different paths to the Android Preferences folder`
when both `ANDROID_PREFS_ROOT` and `ANDROID_USER_HOME` are exported. Unsetting them makes debug and
release builds succeed. Recorded in `KNOWN_ISSUES.md` §2.

## 5. Remaining Blockers

1. Android upload keystore + 4 CI secrets (`KEYSTORE_SETUP.md`) and a physical Android device.
2. macOS + Xcode + Apple account, and a physical iPhone.
3. DNS name, reachable host and Caddy for CA-trusted TLS; physical devices for the pairing/revocation
   steps.
4. Linux 64-bit CGO CI runner for `-race` (job exists; must actually run on push).
5. M2–M6 remain entirely outstanding (identity, persistence, observability, scale, distribution).
