# DevOrbit — Known Issues, Risks & Limitations

## 1. Closed / Remediated

### Master Plan 26 / M2 (this session)

- ~~**C1** Control Plane exposed unauthenticated machine enumeration/registration, token issuance,
  revocation and `session_secret` responses~~ — **RESOLVED by removal**: `services/control-plane` and
  its Dockerfile deleted; compose service and CI matrices updated; README/OpenAPI annotated. Zero
  external imports existed.
- ~~Duplicate broker implementation with divergent security behaviour~~ — **RESOLVED**: canonical broker
  is `services/connection-broker/pkg/broker`; `internal/broker` deleted. The Host Bridge E2E now
  consumes the v1.2 `session_ready`/`session_pending` control frames like the mobile client.
- ~~Broad binary-name-only command allowlist (`git`/`gradle`/`adb` could execute arbitrary code via
  `-c`, `-I`, `adb shell`)~~ — **RESOLVED**: centralized argument-aware policy with per-family
  sub-command allowlists, denied options/prefixes, path/identity containment, size/UTF-8/control-char
  limits and environment sanitisation.
- ~~No structured security logging / no secret-redaction guarantees~~ — **RESOLVED**:
  `pkg/observability` JSON logger with name+value redaction, used by both services.
- ~~No metrics/health semantics; health endpoint returned static JSON~~ — **RESOLVED**: dependency-free
  Prometheus registry, `/metrics` behind admin auth, `live`/`ready` separated; readiness is 503 until
  security initialisation completes.
- ~~Compose: committed DB password, unused control-plane service, broker published on all interfaces,
  Dockerfile Go version mismatch (`golang:1.24` vs `go 1.26`), root container~~ — **RESOLVED**:
  loopback-only publish, required broker secret (fail-fast), Go 1.26 image, non-root user, read-only
  rootfs, healthcheck, Postgres behind `future-db` profile.

### Master Plan 25 / M1

- `ConnectionManager` simulated connectivity (G-12); Android debug signing (G-H3/G-28); missing iOS
  local-network declaration (G-29); CI coverage gaps (G-27); TLS deployment prerequisites (G-10).

### Master Plan 24.1 / 24

- Session tombstone durability, Windows DPAPI per-session deletion, rollback error propagation,
  broker revocation propagation, broker session-only deauthorization; permission persistence, plaintext
  session secrets, HTTPS→HTTP mobile fallback, multi-valued `X-Forwarded-Proto`, Linux cross-compile.

### Master Plan 26.1 (this session)

- ~~Command-policy bypass via attached short-option values (`git -ccore.pager=sh`, `gradle -Ievil.gradle`,
  `-bbuild.gradle`, `-p/tmp/x`, `-g/tmp/x`, clusters such as `-IPevil.gradle`)~~ — **RESOLVED**: structural
  flag parsing (name-based long options, character-scan short clusters, value-consumption aware).
- ~~Gradle executed over HTTP with only session permission while the agent path required approval~~ —
  **RESOLVED**: Gradle is approval-gated on both paths via a session+device+workspace+command+args
  bound, single-use, 10-minute token (`POST /api/v1/processes/approvals`).
- ~~`ResolveExecutable` used a lexical containment check only~~ — **RESOLVED**: real-filesystem
  containment with canonicalized root, resolved-target check and regular-file requirement.
- ~~`docker compose config` failed even with the optional `future-db` profile inactive
  (`POSTGRES_PASSWORD` required interpolation), which would also have failed the MP26 CI job~~ —
  **RESOLVED**: password defaults to empty and the postgres image fails closed at runtime.
- ~~`git --ext-diff`/`--textconv`/`--no-index`/`--output` escaped the policy; `GIT_EXTERNAL_DIFF`,
  `GIT_PAGER`, `GIT_EDITOR`, `GIT_SEQUENCE_EDITOR`, `GIT_TEMPLATE_DIR` were inherited~~ — **RESOLVED**.

## 2. Active Blockers & Environment Limitations

- **Docker daemon unavailable on the implementation host** — `docker version` fails with
  `failed to connect to the docker API at npipe:////./pipe/dockerDesktopLinuxEngine`. Container-topology
  E2E, `docker compose config` and image build could not be executed locally; CI jobs
  (`deployment-validate`, `broker-binary-e2e`) are the intended evidence path.
- **No GitHub CLI / authenticated API access** — workflow run results for the pushed commit could not
  be inspected; Task I (CI execution verification) remains pending external observation.
- **Go `-race` locally (`BLOCKED`)** — 32-bit-only MinGW (`cc1.exe: 64-bit mode not compiled in`).
  CI `go-race` job uses `ubuntu-latest` with `CGO_ENABLED=1`; first execution unverified.
- **iOS compile/TestFlight (`BLOCKED`)** — no macOS/Xcode/Apple account.
- **Live TLS + physical device validation (`BLOCKED`)** — no DNS/CA certificate/proxy host/devices.
- **Android production signing evidence (`BLOCKED`)** — no upload keystore; M1 verification used an
  ephemeral temporary keystore that was deleted.
- **Android builds require sanitised Android env vars** — unset `ANDROID_PREFS_ROOT` and
  `ANDROID_USER_HOME` before Gradle builds on the development host.

### Master Plan 26.2 (this session)

- ~~`POST /api/v1/processes/approvals` returned a usable `approval_token` in the same response, so
  requesting an approval was indistinguishable from granting it~~ — **RESOLVED**: proposals are now
  pending and grant nothing; only an explicit decision that echoes the proposal digest mints a
  single-use execution token.
- ~~A Gradle execution could be authorized without any proposal being presented to a human~~ —
  **RESOLVED**: the mobile client proposes first and shows the exact command, arguments, workspace,
  reason and expiry before an approve/reject action is possible.
- ~~Approval status responses could disclose the execution token~~ — **RESOLVED**: `Get` clears the
  token; only the decision response carries it, exactly once.
- ~~No end-to-end signed-HTTPS coverage of the approval flow (MP26.1 gap)~~ — **RESOLVED**: 12 TLS +
  signed-request tests exercise the real middleware, including the self-approval regression.

## 3. Accepted Residual Risks (documented, not fixed)

- **The host cannot prove a human rather than a compromised client performed the decision.** The host
  guarantees that execution requires a separate decision bound to the exact proposal it issued, made
  by the owning authenticated session/device, which an AI-generated tool call cannot produce. Proving
  human presence (biometric/attestation) requires platform capability that does not exist in the
  current client; this is a documented residual, not a solved property.
- **The `command_digest` echo is an integrity binding, not a cryptographic proof of human perception.**
  It guarantees a decision applies to the exact host-issued proposal and blocks blind approval of an
  unknown command; it cannot prove the UI rendered it.


- **Gradle two-step approval is a breaking API change for clients that ran builds in one call.**
  `/api/v1/processes/execute` now returns `403 approval_required` for Gradle; the mobile client must
  adopt `POST /api/v1/processes/approvals` then resubmit with `approval_token` (M3 UI work). This is
  deliberate and fail-closed. See `docs/security/COMMAND_POLICY.md` §7.
- **TOCTOU between executable validation and process start** — `ResolveExecutable` canonicalizes and
  verifies the target, then the OS starts the resolved path; a writable workspace can swap the file
  in between. Full elimination needs `openat`/`O_NOFOLLOW`, which is platform-specific.
- **Repository-controlled Git configuration remains a code-execution surface** for diff drivers and
  hooks in a repository the user has already trusted. Option and environment routes are blocked
  (`--ext-diff`, `--textconv`, `GIT_EXTERNAL_DIFF`, `GIT_PAGER`), and hooks are never transferred by
  a clone.
- **`git push --force` remains permitted** (approval-gated where routed through the agent).
- **Windows junction / reparse-point containment is not separately tested.** Containment is decided by
  `filepath.EvalSymlinks`, which resolves junctions, but no Windows-specific junction test exists
  (Windows symlink creation requires Developer Mode; the tests skip when unavailable).


- **Command policy denies some legitimate advanced workflows** by design: `git config/credential/
  alias/filter-branch`, Gradle `-I/--init-script/-b/-p`, and every `adb shell/push/pull` operation.
  Widening these requires an explicit, reviewed policy change plus approval-gating evidence.
- **`git push --force` remains permitted** (approval-gated where routed through the agent); destructive
  intent is a user decision, not a policy bypass.
- **Broker state is single-process and file-persisted**; no distributed registry (M5).
- **Metrics are unlabelled** — deliberate to bound cardinality; per-device/per-session breakdowns are
  not available.
- **No request/trace correlation ids on every log line** — `request_id` appears where the flow carries
  one (idempotency), not universally.
- **R8/minify remains disabled** for Android release pending device validation.
- **iOS LAN HTTP** permitted only via `NSAllowsLocalNetworking`; production must use HTTPS.
- **No notifications** (`POST_NOTIFICATIONS` intentionally not declared).
- **Foreground-only task streaming on iOS**.
- **DPAPI orphaned sealed-blob cleanup** still outstanding (M6).
- **PostgreSQL schema remains unused**; moved behind a `future-db` compose profile (M4).

## 4. Protocol & Scope Notes (Deferred / Unchanged)

- Development-mode LAN HTTP pairing stays enabled when `DEVORBIT_ENV != production`.
- `device_public_key` retained for wire compatibility only.
- `packages/design-tokens`, `packages/domain-models` remain stubs.
- `packages/api-contracts/openapi.yaml` still documents some removed control-plane paths; annotated as
  deprecated in M2 and scheduled for a full contract rewrite in M4.
