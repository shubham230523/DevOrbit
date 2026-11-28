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

## 3. Accepted Residual Risks (documented, not fixed)

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
