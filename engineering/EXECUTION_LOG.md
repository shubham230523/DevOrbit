# DevOrbit — Execution Log (Append-Only)

---

### Entry 1 — Master Plan 23 (Historical Baseline)
- **Commit SHA:** `efee2c99e16427223c803b89460dbe61fd151064`
- **Branch:** `security/atomic-agent-task-execution` / `quality/autonomous-15min-improvement`
- **Summary:** Enforced Host Bridge HTTP authentication boundary, E2E tunnel (`/api/v1/e2e`), `SessionStore` with expiry/revocation/permissions, and per-session ownership for processes and agent tasks.
- **Status:** `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED`.

---

### Entry 2 — Master Plan 24: Pairing Bootstrap & Host-Admin Transport Confidentiality
- **Baseline:** `efee2c99e16427223c803b89460dbe61fd151064`
- **Commit SHA:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Branches Pushed & Remote-Verified:**
  - `origin/security/atomic-agent-task-execution` -> `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
  - `origin/quality/autonomous-15min-improvement` -> `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Files Affected (22 files):**
  - Go (`agents/host-bridge`): `cmd/host-bridge/main.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/security/crypto.go`, `internal/security/pairing.go`, `internal/security/pairing_test.go`, `internal/security/session_store.go`, `internal/transport/http/auth_middleware.go`, `internal/transport/http/handlers.go`, `internal/transport/http/pairing_handlers.go`, `internal/transport/http/trusted_proxy.go`, `internal/transport/http/auth_security_test.go`, `internal/transport/http/ai_integration_test.go`, `internal/transport/http/transport_test.go`, `internal/transport/http/pairing_transport_security_test.go` (new), `tests/e2e/pairing_integration_test.go`
  - Flutter (`apps/mobile`): `lib/core/security/e2e_session.dart`, `lib/core/network/direct_e2e_http_client.dart`, `lib/core/security/secure_storage.dart`, `lib/core/pairing/pairing_client.dart`, `test/direct_e2e_http_client_test.dart`, `test/pairing_integration_test.dart`
- **Commands Executed & Outcomes:**
  - `agents\host-bridge`: `go vet ./...` and `go test -count=1 -timeout=120s ./...` -> **PASS** (all packages)
  - `services\connection-broker`: `go vet ./...` and `go test -count=1 ./...` -> **PASS**
  - `services\control-plane`: `go vet ./...` and `go test -count=1 ./...` -> **PASS**
  - `agents\ai-router`: `go vet ./...` and `go test -count=1 ./...` -> **PASS**
  - `agents\acp-adapter`: `go vet ./...` and `go test -count=1 ./...` -> **PASS**
  - `apps\mobile`: `flutter analyze` (No issues found) and `flutter test` (+32: All tests passed) -> **PASS**
- **Stage Status:** `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` (automated suites).
- **Remaining Work:** Independent post-implementation review and live TLS/mobile-to-host smoke validation for Master Plan 24 sign-off.

---

### Entry 3 — Master Plan 0: Persistent Engineering Memory & Cross-Session Continuity
- **Baseline:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4` (preserving Master Plan 24 baseline `efee2c99e16427223c803b89460dbe61fd151064`)
- **Changes Made:** Created `AGENTS.md`, `engineering/PROJECT_CONTEXT.md`, `engineering/CURRENT_STATE.md`, `engineering/ACTIVE_PLAN.md`, `engineering/DECISIONS.md`, `engineering/KNOWN_ISSUES.md`, `engineering/EXECUTION_LOG.md`, `engineering/handoffs/latest-handoff.md`, and `engineering/handoffs/archive/.gitkeep`. Zero application code modified.
- **Status:** `IN_PROGRESS` (pending commit, push, and remote SHA verification).

---

### Entry 4 — Master Plan 0 Completion & Master Plan 0.1 Continuity State Reconciliation
- **Supersedes:** Entry 3's preliminary `IN_PROGRESS` status (Master Plan 0 was committed, pushed, and remote-verified at `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`).
- **Master Plan 0 Commit SHA:** `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`
- **Master Plan 24 Implementation Commit SHA:** `7ba545da10f4ec61bf180c70c60960a3af03e5d4`
- **Master Plan 0 Status:** `COMPLETED` (`COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` on `origin/security/atomic-agent-task-execution` and `origin/quality/autonomous-15min-improvement`).
- **Master Plan 0.1 Changes Made:** Reconciled `CURRENT_STATE.md`, `ACTIVE_PLAN.md`, `EXECUTION_LOG.md`, `KNOWN_ISSUES.md`, and `handoffs/latest-handoff.md` to mark Master Plan 0 completed, remove obsolete MP0 commit/push instructions, record `7aed09683a829f83dd3a6f04cfbba0f6e376bc31` as the verified MP0 commit, preserve `7ba545da10f4ec61bf180c70c60960a3af03e5d4` as MP24's automated implementation commit, and document unresolved `session_store.go` persistence/empty-permission restoration risks and `pairing_client.dart` HTTPS-to-HTTP fallback behavior for independent review. Zero application code or tests modified.
- **Remaining Work:** Independent review of Master Plan 24 (`7ba545da10f4ec61bf180c70c60960a3af03e5d4`) and live TLS/mobile-to-host validation.

---

### Entry 5 — Master Plan 24 Closure (Independent Review & Targeted Remediation)
- **Starting Baseline (MP0.1):** `be4ec8aa93cdfb8463f393f9f5d1ee0d457ef60d`
- **Commit / Branch:** Pending commit & push on `security/atomic-agent-task-execution` & `quality/autonomous-15min-improvement`
- **Confirmed Findings Remediated:**
  - **F-01 (High):** Empty/missing permissions escalation in `SessionStore.LoadPersistedSessions` -> removed `omitempty`, preserved empty `{}` maps as zero-privilege `NewScopedSessionPermissions()`, rejected `nil` permissions and legacy plaintext files fail-closed (`ErrCorruptPersistedState`).
  - **F-02 (High):** Plaintext `session_secret` persistence, ignored I/O errors, non-atomic writes, and non-durable revocation in `SessionStore` (`host_http_sessions.json`) and `BrokerClient` (`host_sessions.json`) -> added OS-backed `SecretStore` (`secret_store.go`, `secret_store_windows.go` DPAPI, `secret_store_linux.go` `secret-tool`, `secret_store_other.go`), memory-only fallback on unavailable OS store, atomic `WriteFileAtomic`, durable `revoked_sessions`/`revoked_devices` tombstones, error propagation across callers (`main.go`, `handlers.go`, `pairing_handlers.go`).
  - **F-03 (High):** Mobile `PairingClient` hardcoded `http://` fallback on `SocketException` -> restricted `fallbackUris` strictly to `scheme == 'http'`.
  - **F-04 (Low):** Multi-valued `X-Forwarded-Proto` header ambiguity -> required `len(r.Header.Values("X-Forwarded-Proto")) == 1` in `isTransportConfidential`.
  - **F-05 (Cross-Platform Build):** `cmd/host-bridge/main.go` `syscall.NewLazyDLL` broke `GOOS=linux` builds -> extracted `console_windows.go` and `console_other.go`.
- **Verification Commands Executed (after final code edit):**
  - `go vet ./...` & `go test -count=1 -timeout=120s ./...` in `agents\host-bridge`, `services\connection-broker`, `services\control-plane`, `agents\ai-router`, `agents\acp-adapter` -> **PASS** (exit 0).
  - `GOOS=linux GOARCH=amd64 go build ./...` & `GOOS=windows GOARCH=amd64 go build ./...` in all 5 Go modules -> **PASS** (exit 0).
  - `go test -race -count=1 -timeout=180s ./internal/security/...` in `agents\host-bridge` -> **BLOCKED** (exit 1: `cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`).
  - `flutter analyze` -> **PASS** (`No issues found!`).
  - `flutter test` -> **PASS** (`+37: All tests passed!`, including 4 new HTTPS fallback regression tests & Go/Dart HTTP Auth MAC vector test).
- **Phase 4 Live Validation Status:** **BLOCKED** on `Windows NT 10.0.26200.0` (no `caddy`/`nginx`, no trusted CA cert, `adb devices -l` empty).
- **Stage Status:** Code & automated tests `IMPLEMENTED`, `TESTED`, `REVIEWED`; `PRODUCTION-VALIDATED` remains `BLOCKED`.

---

### Entry 6 — Master Plan 24.1: Revocation Durability & Rollback Failure Closure
- **Verified Starting HEAD:** `2425cc838c64e7e1e28c67e33c1636d45e5889d7` (confirmed via `git rev-parse HEAD`; both `origin/security/atomic-agent-task-execution` and `origin/quality/autonomous-15min-improvement` matched it; `origin/main` = `c49804547aef7dc1100b8b444ee32695f353e7e6`, untouched).
- **Working-Tree Reconciliation:** the session start found 10 modified files of unfinished MP24.1 work left uncommitted by a previous session plus one untracked Windows test file. They were inspected against source and Git, corrected, extended, and committed — not discarded.
- **Code + Test Commit:** `76ca2286523b1978acec1e64723d74ec64cddf2d` (13 files, +1414/−97).
- **Changes Made (IMPLEMENTED):**
  - `session_store.go`: `RevokeSession` now writes a durable idempotent tombstone for sessions absent from memory, destroys the sealed secret referenced by persisted state before writing the tombstone, and documents the boolean contract. `saveLocked` merges persisted-only entries (previously it erased every durable session not loaded into memory). `RemoveSession`/`RevokeDevice` also destroy sealed secrets referenced only by persisted state; new helper `persistedSecretRefsLocked` + `readPersistedEntries`.
  - `secret_store_windows.go`: new `windows-dpapi-file-v2` store writing each DPAPI-sealed secret to an external `0600` per-session file referenced by an opaque 32-hex id, giving a real `Delete()`; unsafe scopes and malformed refs fail closed.
  - `pairing_handlers.go`/`handlers.go`/`broker_client.go`/`broker_authorizer.go`/`cmd/host-bridge/main.go`: `SessionRevoker` returns errors; revocation errors propagate; HTTP returns `500` on durability failure and `502` on remote failure; rollback revokes the in-memory BrokerClient session, reports rollback failures alongside the original error, and never returns a pairing success after partial provisioning; failed provisioning restores a prior device-revocation tombstone.
  - `services/connection-broker/internal/broker/broker.go`: added `Broker.RevokeSession(machineID, sessionID)` (deauthorizes the session's client token/expiry, closes tunnels, notifies client) and `HandleRevoke` now uses it for `session_id`-only requests — the existing endpoint is reused, no new endpoint invented.
- **Tests Added (TESTED):** `TestSessionStore_AbsentSessionRevocationTombstoneAndRollback`, `TestSessionStore_AbsentRevocationDestroysPersistedSealedSecret`, `TestSessionStore_RevocationPreservesUnloadedPersistedSessions`, `TestWindowsDPAPISecretStore_{StoreLoadDeleteAndFailClosed,RevokeAndRollbackBlockStaleFileRecovery,ErrorPropagationAndFailClosed}`, `TestBrokerClient_PersistenceAndRevocationErrorPropagation`, `TestPairingRollbackAndBrokerRevocationErrorPropagation`, `TestBroker_SessionOnlyRevocationIsolatedFromSiblingSessions`.
- **Verification Commands Executed (after the final code edit):**
  - `agents\host-bridge`: `go vet ./...` -> **PASS** (exit 0); `go test -count=1 -timeout=180s ./...` -> **PASS** (12/12 packages).
  - `services\connection-broker`: `go vet ./...` + `go test -count=1 -timeout=120s ./...` -> **PASS** (exit 0).
  - `services\control-plane`, `agents\ai-router`, `agents\acp-adapter`: `go vet ./...` + `go test ./...` -> **PASS** (exit 0).
  - `GOOS=linux GOARCH=amd64` & `GOOS=windows GOARCH=amd64` `go build ./...` in `agents\host-bridge` and `services\connection-broker` -> **PASS** (exit 0).
  - Native Windows DPAPI tests: `go test -run TestWindowsDPAPI -v ./internal/security/` -> **PASS** (3 tests).
  - `go test -race -count=1 -timeout=300s ./internal/security/...` -> **BLOCKED** (exit 1: `# runtime/cgo` / `cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`; only 32-bit MinGW installed).
  - `apps/mobile`: `flutter analyze` -> **PASS** (`No issues found!`); `flutter test` -> **PASS** (`+37: All tests passed!`).
- **Stage Status:** `IMPLEMENTED`, `TESTED`, `REVIEWED`, `COMMITTED` (`76ca2286523b1978acec1e64723d74ec64cddf2d`).
- **Not Closed:** Master Plan 24 Criterion I (`PRODUCTION-VALIDATED`) and the `-race` suite remain `BLOCKED` on this host; MP24 is **not** declared fully closed or production-ready.

---

### Entry 7 — Master Plan 24.1 Commit, Push & Remote Verification
- **Commits Produced:**
  - `76ca2286523b1978acec1e64723d74ec64cddf2d` — code + tests (13 files).
  - `d71166253da768a522fed27bb0ea2fda06ccce7e` — continuity documents (`CURRENT_STATE.md`, `ACTIVE_PLAN.md`, `KNOWN_ISSUES.md`, `EXECUTION_LOG.md`, `handoffs/latest-handoff.md`, archived prior handoff).
- **Commands Executed and Observed Results:**
  - `git push origin security/atomic-agent-task-execution` -> `2425cc8..d711662  security/atomic-agent-task-execution -> security/atomic-agent-task-execution` (exit 0).
  - `git push origin HEAD:quality/autonomous-15min-improvement` -> `2425cc8..d711662  HEAD -> quality/autonomous-15min-improvement` (exit 0).
  - `git ls-remote origin refs/heads/security/atomic-agent-task-execution refs/heads/quality/autonomous-15min-improvement refs/heads/main` ->
    ```text
    c49804547aef7dc1100b8b444ee32695f353e7e6    refs/heads/main
    d71166253da768a522fed27bb0ea2fda06ccce7e    refs/heads/quality/autonomous-15min-improvement
    d71166253da768a522fed27bb0ea2fda06ccce7e    refs/heads/security/atomic-agent-task-execution
    ```
- **Remote Verification:** both engineering branches matched local `HEAD` (`d71166253da768a522fed27bb0ea2fda06ccce7e`) at verification time; `refs/heads/main` remained `c49804547aef7dc1100b8b444ee32695f353e7e6` (untouched). No force-push was used.
- **Note:** this Entry 7 is a documentation-only commit that follows `d711662`; it changes no source or test file, and both engineering branches were re-pushed and re-verified after it.
- **Stage Status:** `IMPLEMENTED`, `TESTED`, `REVIEWED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` for Master Plan 24.1 Scope 1–7 (automated). `PRODUCTION-VALIDATED` remains `BLOCKED`.

---

### Entry 8 — Master Plan 25 · M1: Verification Closure, Release Build Integrity & Store-Critical Platform Config
- **Starting Baseline (verified):** `6e93ea43d815182bddcc6497df90c16f8aebcbda` (clean working tree, branch `security/atomic-agent-task-execution`).
- **Assessment basis:** `DEVORBIT_PRODUCTION_READINESS_MASTER_ASSESSMENT.md` (MP25 Parts 1–17). No re-audit performed.
- **TASK A — ConnectionManager de-simulation (G-12):**
  - `lib/core/network/connection_manager.dart` rewritten: it now *owns* a real `BrokerWebSocketClient` (injected via `BrokerConnectionFactory`) and derives every status from transport events. Guarantees: `isTransportAuthenticated` only after a broker `session_ready`/`auth_ack`; revoked/expired are terminal; duplicate `connect()` calls are refused; generation counter discards stale async events; bounded exponential backoff with jitter (`computeBackoffDelay`); explicit `reconnect(brokerUrl:, mode:)` for Wi-Fi→mobile transitions; deterministic teardown on `disconnect()`/`dispose()`.
  - `lib/core/network/broker_websocket_client.dart` hardened: added `autoReconnect`/`baseBackoff`/`maxBackoff`/`jitterRatio`/`Random` injection, generation guard on all async callbacks, `_connectInFlight` duplicate guard, jittered backoff, terminal-state sticky guard (`connect()` refuses after revoked/expired; `resetTerminalState()` for re-pairing), config errors (`ArgumentError`/`FormatException`) now reported as `connectionFailed` rather than `authFailed`, and unexpected closure is surfaced as `connectionFailed` when self-reconnect is disabled so a supervisor always observes the loss.
  - `lib/features/dashboard/presentation/dashboard_screen.dart`: UI status now comes from the supervised `ConnectionManager`; `sendRequest` goes through `manager.client` guarded by `isTransportAuthenticated`. No code path can render "connected" without an authenticated transport.
  - `test/connection_manager_test.dart` (new, 12 tests) drives **real** loopback WebSocket servers (`HttpServer` + `WebSocketTransformer`): premature-auth prevention, `session_ready` gating, 401 auth failure + retry, unexpected close → reconnect → recover, revocation terminal + refused reconnect, `host_disconnected`, duplicate-connect prevention, manual-disconnect no-retry, late-frame rejection, network-path switch, backoff bounds/jitter determinism, incomplete-profile refusal.
- **TASK B — Android release integrity (G-H3/G-28):** keystore resolution from env vars or git-ignored `key.properties`; release `signingConfig` is `null` without credentials and a `gradle.taskGraph.whenReady` guard **fails the build** (verified); v2/v3/v4 signatures, v1 disabled; `android:label="DevOrbit"`; `allowBackup=false` + `backup_rules.xml` + `data_extraction_rules.xml` excluding secure storage; `networkSecurityConfig` (release: cleartext **disabled**; debug source set: cleartext enabled for LAN dev only); `usesCleartextTraffic=false`; camera `uses-feature required=false`; `minSdk = maxOf(flutter.minSdkVersion, 24)`; R8 minify deliberately left off pending device validation (rules prepared).
  - `KEYSTORE_SETUP.md`, `key.properties.example`, `app/proguard-rules.pro` added; root `.gitignore` extended to ignore `*.jks`, `*.keystore`, `key.properties`.
- **TASK C — iOS store config (G-29/G-41):** `NSLocalNetworkUsageDescription` added; ATS set to `NSAllowsLocalNetworking=true` only (**no** `NSAllowsArbitraryLoads`); camera description expanded; no `UIBackgroundModes` (background execution deliberately not promised); `IOS_STORE_READINESS.md` documents prerequisites, foreground-only task behavior, and the BLOCKED Xcode/TestFlight state.
- **TASK D — CI (G-27):** `.github/workflows/ci.yml` rewritten: triggers `main`, `security/**`, `quality/**`, `feature/**`; `permissions: contents: read` with least privilege; concurrency cancel; matrix over all 5 Go modules for `go vet` + `go test`; **`go test -race` on ubuntu-latest with `CGO_ENABLED=1`**; linux/windows cross-builds; `flutter analyze` + `flutter test`; Android debug build with artifact upload; signed release build gated on `github.event_name == 'push'` + secret presence (never on PRs), with `apksigner` verification rejecting a debug-key signature; gitleaks secret scan; `govulncheck` per module.
- **TASK E — TLS deployment prerequisites (G-10):** `infrastructure/deployment/Caddyfile` (TLS/HSTS, `wss` upgrade with buffering disabled and no read/write timeout, health checks, single `X-Forwarded-Proto`), `env.example`, rewritten `deployment/README.md`, and `docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md` (executable steps + evidence table, currently PENDING/BLOCKED).
- **TASK F — Hygiene & continuity:** confirmed **no** tracked binaries, keystores, `key.properties`, build outputs or `tests/` entries (`git ls-files` checks); local empty `tests/` directory removed; continuity documents updated.
- **Verification Commands Executed (after the final edit):**
  - `go vet ./...` + `go test -count=1 ./...` in all 5 Go modules → **PASS** (exit 0).
  - `GOOS=linux` / `GOOS=windows` `go build ./...` in `agents/host-bridge` and `services/connection-broker` → **PASS** (exit 0).
  - `flutter analyze` → **PASS** (`No issues found!`, 49.2 s).
  - `flutter test` → **PASS** (`+49: All tests passed!` — 37 pre-existing + 12 new ConnectionManager tests).
  - `flutter build apk --debug` → **PASS** (`Built build\app\outputs\flutter-apk\app-debug.apk`).
  - `flutter build apk --release` without credentials → **PASS (expected failure)**: `DevOrbit release build refused: no release signing credentials configured.`
  - `flutter build apk --release` with an **ephemeral** keystore in `%TEMP%` (deleted immediately after) → **PASS** (`Built build\app\outputs\flutter-apk\app-release.apk (14.5MB)`); no production credential created or committed.
  - `go test -race` on this Windows host → **BLOCKED** (32-bit-only MinGW; CI job added to run it on ubuntu-latest).
  - iOS compile/TestFlight → **BLOCKED** (no macOS/Xcode).
  - Live TLS + physical device validation → **BLOCKED** (no DNS/cert/proxy host/devices).
- **Environment defect discovered (not a code defect):** Gradle failed with `AndroidLocationsException: Several environment variables … contain different paths to the Android Preferences folder` while both `ANDROID_PREFS_ROOT` and `ANDROID_USER_HOME` were exported. Builds succeed after unsetting those variables. Documented in `KNOWN_ISSUES.md`.
- **Stage Status:** M1 repository scope `IMPLEMENTED`, `TESTED`; external-evidence scope `BLOCKED`. Verdict: **M1 PARTIALLY COMPLETE**.

---

### Entry 9 — MP25 M1 Commit, Push & Remote Verification
- **M1 Implementation Commit:** `678c923575bd29b4e49b71fc848db0da38752929` (27 files, +2423/−480).
- **Push Commands and Observed Results:**
  - `git push origin security/atomic-agent-task-execution` → `6e93ea4..678c923  security/atomic-agent-task-execution -> security/atomic-agent-task-execution` (exit 0).
  - `git push origin HEAD:quality/autonomous-15min-improvement` → `6e93ea4..678c923  HEAD -> quality/autonomous-15min-improvement` (exit 0).
- **`git ls-remote origin` (verified after push, exit 0):**
  ```text
  c49804547aef7dc1100b8b444ee32695f353e7e6        refs/heads/main
  678c923575bd29b4e49b71fc848db0da38752929        refs/heads/quality/autonomous-15min-improvement
  678c923575bd29b4e49b71fc848db0da38752929        refs/heads/security/atomic-agent-task-execution
  ```
  Both engineering branches matched local `HEAD` (`678c923…`); `refs/heads/main` remained `c49804547aef7dc1100b8b444ee32695f353e7e6` (untouched). No force-push used.
- **Note:** this Entry 9 is a documentation-only commit following `678c923`; both engineering branches were re-pushed and re-verified afterwards.
- **External evidence still outstanding (unchanged):** physical Android device, macOS/Xcode + iPhone, DNS + CA-trusted certificate + proxy host, Android upload keystore and CI signing secrets, Linux `-race` execution on the pushed workflow.

---

### Entry 10 — MP26 / M2: Security Hardening & Operations Baseline
- **Starting Baseline (verified):** `881c7dce958ffd2723ba4a7880613a89939fd33d`, clean tree, branch `security/atomic-agent-task-execution`.
- **Task A — Control Plane disposition (REMOVED):** `services/control-plane/**` (module, cmd, internal/auth, internal/pairing, tests) and `infrastructure/docker/Dockerfile.control-plane` deleted; compose service and all 3 CI matrix entries removed; README and `packages/api-contracts/openapi.yaml` updated. Evidence for removal: zero imports outside the module, no mobile/broker reference, and it exposed unauthenticated machine enumeration/registration, token issuance, revocation and `session_secret` responses.
- **Task B — Legacy broker disposition (CONSOLIDATED):** the canonical implementation now lives in `services/connection-broker/pkg/broker` (moved from `internal/broker`, including all 3 test files); `internal/broker` deleted; `cmd/connection-broker` and the Host Bridge pairing E2E import `pkg/broker`. Exactly one broker implementation remains. The Host Bridge E2E was aligned with the canonical v1.2 protocol (consume `session_ready`/`session_pending` control frames before application data) — the old duplicate omitted that frame.
- **Task C — Per-command security policy:** new `internal/security/command_policy.go` is the single argument-aware policy for every execution boundary: executable identity (bare allowlisted name or `./wrapper` only; absolute/drive/UNC/sub-directory/traversal rejected), per-family sub-command allowlists (git, adb) and Gradle task semantics, denied option lists/prefixes (`git -c/--config-env/--git-dir/--work-tree/--exec/--upload-pack/--receive-pack/--open-files-in-pager/--exec-path`, `gradle -I/--init-script/-b/--build-file/-p/--project-dir/--include-build/--gradle-user-home/--project-cache-dir`, adb `shell/push/pull/install/root/remount/forward/reverse/tcpip/connect/logcat/reboot/sideload`), argument count/length/UTF-8/control-character limits, `ResolveExecutable` workspace containment, and `SanitizeEnvironment` (LD_*/DYLD_*/GIT_DIR/GIT_WORK_TREE/GIT_SSH_COMMAND/GIT_CONFIG_*/BASH_ENV/JAVA_TOOL_OPTIONS/GRADLE_OPTS/NODE_OPTIONS…) applied by `ProcessRunner`. Errors never echo argument values.
- **Task D — Structured logging:** new shared package `services/connection-broker/pkg/observability` (JSON lines, `kind=security|operational`, service/version/env metadata, level filtering, field-name **and** value-shape redaction, truncation, composite summary, nil-safe, concurrency-safe) imported by both the Host Bridge and the broker. Wired events: pairing success/failure + reason codes, rate limiting, authorization/authentication denials, revocation success/failure, command-policy rejection, startup/shutdown, broker lifecycle/auth rejections.
- **Task E — Metrics & health:** dependency-free Prometheus text registry (counters + callback gauges, strict low-cardinality name validation, invalid names rejected and counted, nil-safe). Host Bridge exposes `/api/v1/health/live`, `/api/v1/health/ready` (503 until security init completes) and host-admin-protected `/metrics`; broker exposes `/api/v1/health/live`, `/api/v1/health/ready` and admin-protected `/metrics`. Gauges: `devorbit_uptime_seconds`, `devorbit_active_broker_sessions`, `devorbit_broker_active_sessions`, `devorbit_broker_uptime_seconds`.
- **Task F — Deployment hardening:** `Dockerfile.connection-broker` now builds with `golang:1.26-alpine` (matching `go.mod`), trims symbols, runs as non-root, and has a liveness healthcheck; `docker-compose.yml` publishes the broker on loopback only, requires `DEVORBIT_BROKER_SECRET` (fail-fast), adds healthcheck/read-only/no-new-privileges/tmpfs, removes the control-plane service, and moves PostgreSQL behind an explicit `future-db` profile; new `infrastructure/docker/.env.example`.
- **Task G — Executable E2E (container blocked):** new build-tagged `services/connection-broker/tests/e2e` runs the **real broker binary**: production-without-secret fail-closed, liveness/readiness, admin-protected metrics (401 unauthenticated, 200 authenticated, secret not leaked) and process termination. Added CI job `broker-binary-e2e`. Docker daemon is unavailable on this host, so the container-topology scenarios are **BLOCKED**; CI job `deployment-validate` runs compose validation, image build and a container smoke test (liveness + protected metrics) on ubuntu-latest.
- **Task H — Contract tests:** new `internal/transport/http/contract_test.go` — 16 protected routes and 3 host-admin routes reject unauthenticated calls with JSON `{"error":…}`, query-string credentials rejected (400), public-route method validation (405), auth-before-method ordering documented on protected/admin routes, readiness semantics (503→200), no `session_secret`/token fields in pairing error responses, unknown JSON fields tolerated.
- **Task I — CI execution verification:** workflow updated (control-plane removed from matrices; `broker-binary-e2e` and `deployment-validate` added). Pre-existing jobs (vet/test matrix, `-race` with CGO, cross-builds, Flutter, Android debug, gated signed release, gitleaks, govulncheck) unchanged. Local YAML reviewed; **actual GitHub Actions results could not be inspected** (`gh` CLI absent, Docker daemon down) — see `CURRENT_STATE.md` §5.
- **Verification Commands Executed (after the final edit):**
  - `go vet ./...` + `go test -count=1 ./...` in `agents/host-bridge` → **PASS** (14 packages).
  - `go vet ./...` + `go test -count=1 ./...` in `services/connection-broker` → **PASS**.
  - `go test -tags=e2e -count=1 ./tests/e2e/` (broker, real binary) → **PASS** (2 tests, 9.5 s).
  - `agents/ai-router`, `agents/acp-adapter`: vet + test → **PASS**. `services/control-plane` no longer exists.
  - `GOOS=linux` / `GOOS=windows` `go build ./...` in host-bridge, broker, ai-router → **PASS**.
  - `flutter analyze` → **PASS** (`No issues found!`); `flutter test` → **PASS** (`+49: All tests passed!`).
  - Docker/container E2E locally → **BLOCKED** (daemon not running: `npipe:////./pipe/dockerDesktopLinuxEngine` not found).
  - GitHub Actions run inspection → **BLOCKED** (`gh` CLI not installed; no authenticated API access from this host).
- **Stage Status:** M2 repository scope `IMPLEMENTED`, `TESTED`; container-topology E2E and CI-run inspection `BLOCKED`. Verdict: **M2 PARTIALLY COMPLETE**.

---

### Entry 11 — MP26 M2 Commit, Push & CI Evidence (including a correction to the MP25 CI assumption)
- **M2 Implementation Commit:** `d8eb21fbac222b96b24befc1738605f4fb597f9c` (43 files, +3017/−2123).
- **Push Results:** `881c7dc..d8eb21f` for both `security/atomic-agent-task-execution` and `quality/autonomous-15min-improvement` (exit 0).
- **`git ls-remote origin` (exit 0):**
  ```text
  c49804547aef7dc1100b8b444ee32695f353e7e6        refs/heads/main
  d8eb21fbac222b96b24befc1738605f4fb597f9c        refs/heads/quality/autonomous-15min-improvement
  d8eb21fbac222b96b24befc1738605f4fb597f9c        refs/heads/security/atomic-agent-task-execution
  ```
  Local `HEAD` = `d8eb21f…`; working tree clean; no force-push; `main` untouched.
- **CI execution evidence (GitHub REST API, unauthenticated):**
  - `GET /repos/…/actions/runs?branch=security/atomic-agent-task-execution` → `total_count: 0` (**zero runs** for this branch, including `d8eb21f`).
  - `GET /repos/…/actions/runs?per_page=5` → `total_count: 65`, with every visible run on **`main`**, dated **2026-11-23**, `conclusion: failure` (e.g. `145f8b0`, `db25369`, `f145c04`).
  - `GET /repos/…/actions/permissions` → **HTTP 401** (requires authentication), so repository Actions enablement cannot be confirmed from this host.
- **Correction to the MP25 record:** the MP25 claim that the `go-race` job "will execute on ubuntu-latest" was an *expectation*, not evidence. In reality **no workflow run has ever been created for the engineering branches**, so the MP25 CI expansion (race, cross-builds, Android build, scanners) is `NOT RUN`, not `PASS`. Root cause requires authenticated repository access.
- **Consequence for Task I:** MP26 Task I is **BLOCKED** — the workflow is implemented and pushed, but its first execution has not occurred and cannot be inspected from this host.

---

### Entry 12 — MP26.1: Command-Policy Bypass Closure, Gradle Authorization, Symlink Containment, CI/Docker Evidence
- **Verified Starting HEAD:** `bc08bb6ebb4ba9ee4fed3f5ab006417733952083`; branch `security/atomic-agent-task-execution`; clean tree; remote engineering heads independently confirmed at `bc08bb6`, `main` at `c4980454`.
- **A — Argument-parser bypasses found and fixed (real defects, demonstrated before fixing):**
  - `-c` was denied only as a whole token, so the valid Git attached form `-ccore.pager=sh` **passed**. Same class: `-C/etc`, Gradle `-Ievil.gradle`, `-bbuild.gradle`, `-cevil.settings`, `-p/tmp/x`, `-g/tmp/x`, and clusters such as `-IPevil.gradle`.
  - Long options were handled only by a hand-maintained `--opt=` prefix list.
  - Fix: `validateFlag` now parses structurally — long options are matched by **name** (split at `=`), short options are scanned **character by character** through clusters with case-sensitive matching, and scanning stops at value-consuming options so a commit message containing `-c` is not misread. Added denied long options `--ext-diff`, `--textconv`, `--no-index`, `--output` (external driver execution and out-of-workspace read/write) and env sanitisation for `GIT_EXTERNAL_DIFF`, `GIT_PAGER`, `PAGER`, `GIT_EDITOR`, `GIT_SEQUENCE_EDITOR`, `GIT_TEMPLATE_DIR`.
  - Tests: `command_policy_bypass_test.go` — attached short values (11 payloads), long `=`/space forms (20 payloads), case sensitivity (`-i` allowed, `-I` denied), legitimate values containing option letters, argument-order variations, approval classification, symlink containment, error-type mapping.
- **B — Gradle execution authorization (new trust boundary, closed using the existing approval architecture):** the agent `execute_command` tool already created an approval proposal unconditionally and consumed a token bound to (task, workspace, tool, command, args) — verified in `agent_tools.go`; no bypass existed there. The **gap** was the direct HTTP path, which executed Gradle with only session permission. Added `CommandPolicy.RequiresApproval` (Gradle family → true, fail-closed for a nil policy), `ProcessSpec.ApprovalToken`, `Server.SetApprovalManager`, `consumeProcessApproval` (session+device+workspace+command+args binding, single-use, 10-minute TTL, constant-time compare) and `POST /api/v1/processes/approvals` (session + `process:execute`) to issue the token. Tokens are wiped from the spec before execution and never logged. Tests: 10 in `process_approval_test.go` (missing token, nil manager, valid consume, replay, argument/command/workspace/session mismatch, expiry, owner-binding determinism, token wipe, shared classification).
  - **Deliberate behaviour change:** single-step Gradle execution over HTTP now returns `403 approval_required`. Documented in `docs/security/COMMAND_POLICY.md` §7 with the two-step flow; mobile UI adoption is M3 work.
- **C — Executable/symlink containment:** `ResolveExecutable` previously used a lexical `filepath.Rel` check only. It now canonicalizes the workspace root (`EvalSymlinks`), requires the target to resolve, requires the **resolved** path to remain inside the canonicalized root, and requires a regular file; the resolved absolute path is executed. Tests cover: normal in-workspace executable, wrapper symlinked outside, wrapper symlinked inside, nested escaping chain, dangling link, directory target, symlinked workspace root, and error-type mapping (all PASS; symlink cases skip only where the OS forbids link creation). TOCTOU residual risk documented.
- **D — CI diagnosis (BLOCKED, cause narrowed):** `GET /repos/…/actions/workflows` → **1 registered workflow**, `DevOrbit CI`, path `.github/workflows/ci.yml`, **state `active`**. `GET /repos/…/actions/runs?branch=security/atomic-agent-task-execution` → `total_count: 0`. `GET /repos/…/actions/runs?per_page=5` → `total_count: 65`, every visible run on `main`, dated **2026-11-23**, `conclusion: failure`. `GET /repos/…/actions/permissions` → **401**. Conclusion: the workflow is registered and active but **no run has been created since 2026-11-23** for any branch, which is consistent with exhausted Actions minutes/spending limit or an account-level Actions policy change; neither is readable without authenticated access, and neither is authorized to be changed by this milestone. Local YAML review performed; no configuration defect identified in the workflow file.
- **E — Docker:** daemon still unavailable (`npipe:////./pipe/dockerDesktopLinuxEngine` not found); image build, container startup and topology E2E remain **BLOCKED**. **Compose validation was executed and found a real defect:** `docker compose config` failed with `required variable POSTGRES_PASSWORD is missing a value` **even with the `future-db` profile inactive**, because Compose interpolates every service before profile filtering — this would also have failed the MP26 `deployment-validate` CI job. Fixed by defaulting `POSTGRES_PASSWORD` to empty (postgres refuses to start without a value, so it still fails closed) with an explanatory comment. Re-verified: `docker compose config --quiet` → **exit 0**; `config --services` → only `connection-broker`; resolved publish → `host_ip: 127.0.0.1`, `published: 8081`.
- **Verification executed after the final edit:** `go vet ./...` + `go test -count=1 ./...` in `agents/host-bridge` → **PASS** (14 packages); `services/connection-broker` → **PASS**; `agents/ai-router`, `agents/acp-adapter` → **PASS**; `GOOS=linux`/`windows` build of host-bridge → **PASS**; `flutter analyze` → `No issues found!`; `flutter test` → `+49: All tests passed!`; `docker compose config` → **PASS**; Docker daemon operations → **BLOCKED**; GitHub Actions run inspection → **BLOCKED**.
- **Stage Status:** MP26.1 workstreams A, B, C `IMPLEMENTED` + `TESTED`; E partially `VALIDATED` (compose); D and container E2E `BLOCKED`. Verdict: **PARTIALLY COMPLETE**.

---

### Entry 13 — MP26.2: Human Approval Integrity, Gradle Authorization & Mobile Integration
- **Verified Starting HEAD:** `2a3051968006695676e6925d5d623e4a5217f1c3`; branch `security/atomic-agent-task-execution`; clean tree; remote engineering heads confirmed at `2a30519`, `main` at `c4980454`.
- **A — Trust boundary traced, MP26.1 concern confirmed as a real flaw:** `HandleCreateProcessApproval` authenticated the session, created an `ApprovalManager` proposal and returned the usable `approval_token` **in the same response**. Request and grant were one operation, so an automated client (and any code able to reach the endpoint with a session) could authorize Gradle execution without any human decision, and the proposal was never presented to a person. Two paths were traced: the agent `execute_command` tool (already required a proposal + bound token; the AI never receives a usable token) and the direct HTTP path (the weak one).
- **B — Genuine human approval implemented:** new `internal/application/process_approval_store.go` with an explicit lifecycle (`pending → approved | rejected | expired | consumed`). Creating a proposal returns **no capability**; only `POST /api/v1/processes/approvals/{id}/decision` with `decision=approve` and the digest of the proposal the user was shown mints a single-use `execution_token`. Rejection never mints one. Binding covers session, device, workspace, command, argument vector and canonical digest (`sha256` over a length-prefixed, domain-separated encoding so `["a b"]` and `["a","b"]` cannot collide). Single-use, 10-minute TTL, constant-time comparison, token wiped from the request before launch. A dedicated store was required because the task-oriented `ApprovalManager` mints its token at proposal time — the exact flaw being fixed; command classification still comes from the single shared `CommandPolicy`.
- **C — Mobile integration:** new pure-Dart `ProcessApprovalController` state machine plus `HostBridgeProcessApprovalApi` adapter and `HostBridgeApiClient.requestProcessApproval` / `decideProcessApproval` / `executeProcess(approvalToken:)`. `RemoteTasksScreen` now proposes first and shows an approval dialog with the exact command, arguments, workspace, reason and expiry; approve/reject/cancel are explicit; execution is attempted only after a server-issued token; duplicate taps are ignored; network failures never auto-retry an approval and never execute. Non-Gradle commands fall back to the single-step path when the host answers 400.
- **D — Consistent enforcement:** both paths derive the requirement from `CommandPolicy.RequiresApproval` and consume through the same store; the host revalidates the full binding immediately before launch; `wipeApprovalToken` runs before execution so the token cannot reach the child process or a response.
- **E — Signed-HTTPS end-to-end verification (gap closed):** 12 tests in `process_approval_e2e_test.go` run against a real TLS server with cryptographically signed requests (`signTLSHeaders` + `ComputeHTTPRequestMAC` through the production middleware): unauthenticated request rejected; invalid signature and stale timestamp rejected; **request grants no execution capability** (the MP26.1 regression); authenticated session alone cannot execute; approval requires the proposal digest; explicit approval permits the exact command and replays are rejected; rejection prevents execution and cannot be flipped; modified arguments and modified workspace invalidate; another session cannot decide or consume; expired approval returns 410; revoked session cannot consume; 8 concurrent duplicate executions produce exactly one success; non-Gradle commands unaffected. Plus `TestAgentExecuteCommand_GradleCannotExecuteWithoutHumanApproval` for the AI path.
- **F — Verification executed after the final edit:** host-bridge `go vet` + `go test -count=1 ./...` → **PASS** (14 packages); process approval store tests (12, incl. 64-goroutine consume race and 32-goroutine decide race) → **PASS**; approval E2E (12) → **PASS**; broker, ai-router, acp-adapter → **PASS**; `GOOS=linux`/`windows` host-bridge build → **PASS**; `flutter analyze` → `No issues found!`; `flutter test` → **`+63: All tests passed!`** (49 previous + 14 new approval-controller tests).
- **Defects found in my own MP26.1 code while testing:** the approval status accessor returned the execution token (now cleared in `Get`), and the store/`Verify` expiry test exposed an arithmetic error in the test itself (fixed, not weakened).
- **G — Documentation:** `docs/security/COMMAND_POLICY.md` §7 rewritten (propose/decide/execute protocol, states, error table, token properties, mobile flow, migration); `KNOWN_ISSUES.md` and the handoff updated.
- **Retained blockers (unchanged, not re-investigated):** GitHub Actions has created no run for the engineering branches (registered + `active`, newest run 2026-11-23 on `main`); Docker daemon unavailable; Linux symlink runtime and Windows junction tests not executed; TOCTOU residual risk.
- **Stage Status:** A, B, C, D, E, G `IMPLEMENTED` + `TESTED` + locally `VERIFIED`; CI run observation and container E2E `BLOCKED`. Verdict: **PARTIALLY COMPLETE**.

---

### Entry 14 — MP26.3: Agent Approval Unification, Human Approval Integrity & Execution Lifecycle
- **Verified Starting HEAD:** `087ee8b64d42c837c42c930c4b8f3e3cc7a9e353`; branch `security/atomic-agent-task-execution`; clean tree; both engineering branches confirmed at `087ee8b`; `main` at `c4980454`.
- **A — The gap was real:** the agent `execute_command` tool authorized Gradle entirely through the legacy `ApprovalManager`, and `ProcessService` had no gate at all. A valid legacy task-approval token therefore authorized a Gradle execution, and every future route calling `processSvc.ExecuteProcess` would have bypassed the MP26.2 boundary.
- **A/B — One authoritative boundary:** the approval gate now lives in `ProcessService.ExecuteProcess` (shared by the HTTP route and the agent route): it requires the store, requires the owning session/device, and consumes the authorization through `ProcessApprovalStore.ConsumeAuthorization`, which dispatches `pexec-…` (execution token, HTTP) and `pappr-…` (approval id, agent task flow) to the same consumption logic. The token is wiped before launch. The agent tool now consults only the process store for commands that require approval: it creates a pending proposal (approval id + digest, **no token**), and on resume presents the approval id, which is consumed only if a human decision made the state `approved`. `ApprovalManager` remains for task-level approvals (file edits, `git`/`adb`).
- **A3 — Production wiring:** `main.go` creates **one** `ProcessApprovalStore` and injects it into `serverEngine`, `psSvc` and `agentExecutor`. The engine attaches the task owner to the execution context (`WithTaskOwner`) so an agent-requested proposal is bound to the same session/device that would authorize it over HTTP.
- **A4 — Fail-closed proofs (new tests):** missing store → `ErrApprovalRequirementMissing` on both routes; missing owner binding → `ErrProcessApprovalOwner`; valid legacy `ApprovalManager` token for a Gradle command → rejected; pending approval id without a decision → `ErrProcessApprovalNotApproved`; approval id after rejection/consumption → rejected; replay rejected; non-Gradle commands still use the task flow (proved by token prefix).
- **D — Server-side cancellation + truthful Stop:** new `POST /api/v1/processes/approvals/{id}/cancel` (owner-bound, pending-only, deterministic on repeat, never reveals a token, rejects later approval with 409). New `ProcessApprovalStore.Cancel`. Mobile `ProcessApprovalController.cancel()` is now asynchronous and server-confirmed: it reports `cancelled` only on host confirmation, otherwise `failed` with an explicit message, so the UI never claims a cancellation that did not happen. The Stop button now calls `client.cancelProcess(processId)` (host `POST /api/v1/processes/{id}/cancel`) when a process is running and reports "confirmed by host" only on a 200; if no process started it cancels the pending approval; otherwise it says nothing was active. A run-generation counter prevents a late response from an older run overwriting newer UI state.
- **Defect found in my own new code and fixed:** `ConsumeByApprovalID` reported a consumed/pending proposal as "expired" because the expiry helper also treated terminal states as unusable; expiry is now strictly time-based and each caller reports its own precise terminal state.
- **Verification:** `go vet ./...` + `go test -count=1 ./...` host-bridge → **PASS** (14 packages, includes 6 new agent-approval tests, 4 new store lifecycle tests, 2 new signed-HTTPS tests: missing store fails closed, server-side cancellation); broker, ai-router, acp-adapter → **PASS**; `GOOS=linux`/`windows` build → **PASS**; `flutter analyze` → `No issues found!`; `flutter test` → **`+66: All tests passed!`** (49 + 14 + 3 new cancellation tests).
- **Known functional limitation (documented, not hidden):** the agent *task detail* UI does not yet perform the two-step Gradle approval (decide the process proposal, then resume the task with the approval id). The host contract supports it end to end and it is tested at the service and tool level; the in-app Gradle task approval path therefore remains incomplete, which is why this milestone is **PARTIALLY COMPLETE**.
- **Retained blockers:** no CI run for the engineering branches since 2026-11-23; Docker daemon unavailable; Linux symlink and Windows junction runtime tests not executed; `go test -race` unavailable locally; TOCTOU residual; human presence versus compromised client not attestable.
- **Stage Status:** A, B, C, D `IMPLEMENTED` + `TESTED`; mobile task-detail approval UI and external verification `OUTSTANDING`. Verdict: **PARTIALLY COMPLETE**.
