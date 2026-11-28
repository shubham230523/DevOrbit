# DevOrbit — Known Issues, Risks & Limitations

## 1. Closed / Remediated

### Master Plan 25 · M1 (this session)

- ~~**G-12** `ConnectionManager` simulated connectivity (artificial delay + debug-only heartbeat, unused in production)~~ — **RESOLVED**: rewritten as a real supervisor over `BrokerWebSocketClient`; `isTransportAuthenticated` requires a broker `session_ready`; revocation is terminal; duplicate sockets and stale events are impossible via in-flight + generation guards; backoff is bounded with jitter. Dashboard now renders exclusively from this supervised state.
- ~~**G-H3/G-28** Android release builds signed with the debug keystore~~ — **RESOLVED**: credential-driven release signing; `release.signingConfig` is `null` without credentials and a Gradle task-graph guard fails the build (verified: refusal message observed; success observed with an ephemeral keystore).
- ~~Release label `devorbit_mobile`, unrestricted backup of credential storage, no network security policy~~ — **RESOLVED**: `android:label="DevOrbit"`, `allowBackup=false` + exclusion rules, `networkSecurityConfig` with release cleartext disabled and a debug-only LAN exception.
- ~~**G-29 (partial)** missing iOS local-network declaration / undefined ATS policy~~ — **RESOLVED**: `NSLocalNetworkUsageDescription` added; ATS limited to `NSAllowsLocalNetworking` (no `NSAllowsArbitraryLoads`).
- ~~**G-27** CI ignored `security/**` and `quality/**`, had no `vet`, no race job, no cross-builds, no Android build, no scanning~~ — **RESOLVED** in `.github/workflows/ci.yml` (race on ubuntu-latest with CGO, cross-builds, Android debug + gated signed release with debug-key rejection, gitleaks, govulncheck, least-privilege permissions).
- ~~**G-10 (repo part)** no reverse-proxy/TLS deployment artifacts, no validation procedure~~ — **RESOLVED** in repository: `Caddyfile`, `env.example`, deployment README, executable validation procedure.

### Master Plan 24.1

- Session tombstone durability, Windows DPAPI per-session deletion, rollback error propagation, broker revocation propagation, broker session-only deauthorization (see `EXECUTION_LOG.md` Entry 6).

### Master Plan 24 closure

- Empty-permission escalation, plaintext `session_secret`, HTTPS→HTTP mobile fallback, multi-valued `X-Forwarded-Proto`, Linux cross-compile (see Entry 5).

## 2. Active Blockers & Environment Limitations

- **Android builds require sanitised Android env vars (environment defect, not code):** with both
  `ANDROID_PREFS_ROOT` and `ANDROID_USER_HOME` exported, Gradle aborts with
  `AndroidLocationsException: Several environment variables and/or system properties contain different
  paths to the Android Preferences folder`. Workaround used for all verified builds:
  `Remove-Item Env:ANDROID_PREFS_ROOT; Remove-Item Env:ANDROID_USER_HOME` before
  `flutter build …`. CI (ubuntu-latest) does not set these, so CI is unaffected.
- **Go `-race` on this Windows host (`BLOCKED`):** only 32-bit MinGW (`C:\MinGW\bin\gcc.exe`) is
  installed → `cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`. The CI `go-race` job runs
  it on ubuntu-latest with `CGO_ENABLED=1`; local evidence remains blocked.
- **iOS compile/sign/TestFlight (`BLOCKED`):** no macOS/Xcode, no Apple account. Configuration is
  implemented and documented in `apps/mobile/ios/IOS_STORE_READINESS.md`.
- **Live TLS + physical device validation (`BLOCKED`):** no DNS name, no CA-trusted certificate, no
  reachable proxy host, no attached physical Android/iOS devices. Procedure:
  `docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md` (all rows currently PENDING).
- **Android signed-release *production* evidence (`BLOCKED`):** repository verification used an
  **ephemeral temporary keystore** (created in `%TEMP%`, deleted immediately, never committed). A real
  upload keystore plus Play Console access is required for store evidence.
- **Headless Linux Secret Service UX:** without an active D-Bus Secret Service the Host Bridge refuses
  plaintext secret persistence (`ErrSecureStoreUnavailable`), persists tombstones only, and devices
  must re-pair after restart.

## 3. Accepted Residual Risks (documented, not fixed in M1)

- **R8/minify disabled for release.** Enabling it without a device-validated release risks stripping
  reflectively loaded plugin classes. Rules are prepared in `app/proguard-rules.pro`.
- **iOS local networking only via `NSAllowsLocalNetworking`.** LAN HTTP remains possible for developer
  setups by design; production deployments must use HTTPS (Host Bridge enforces this in
  `DEVORBIT_ENV=production`).
- **No notifications implemented**, therefore `POST_NOTIFICATIONS` is deliberately **not** declared.
- **Foreground-only task streaming on iOS** (no `UIBackgroundModes`); the app re-polls state on resume.
- **No physical erasure of deleted DPAPI secret files**; deleting removes OS-level access only.
- **Orphaned sealed-secret files** may remain if `SecretStore.Delete` fails while the tombstone write
  succeeds (unreachable from persisted state, same-user-sealed only).
- **`services/connection-broker/pkg/broker`** remains a legacy duplicate without the `RevokeSession`
  deauthorization fix; it is compiled but unused by `cmd/connection-broker` (M2 cleanup).
- **Control Plane remains unauthenticated and is not deployed** (M2 decision: rewrite or delete).
- **No page-level UI tests for the rewired dashboard connection states.** Connection state is covered
  by 12 transport-level tests; widget-level coverage of the new binding is a follow-up.

## 4. Protocol & Scope Notes (Deferred / Unchanged)

- **Development-mode LAN HTTP pairing** stays enabled when `DEVORBIT_ENV != production`; production
  blocks plaintext pairing/host-admin endpoints (403).
- **`device_public_key`** retained for wire compatibility only.
- **`packages/design-tokens`, `packages/domain-models`** remain 4-line stubs (deferred).
- **`services/control-plane` schema (`infrastructure/database/schema.sql`)** is unused: no Go module
  imports any SQL driver. Persistence is planned for M4.
