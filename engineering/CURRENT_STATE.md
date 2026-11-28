# DevOrbit — Current State

**Last Updated:** 2026-11-28
**Current Branch:** `security/atomic-agent-task-execution`
**Verified Starting HEAD (MP26 M2):** `881c7dce958ffd2723ba4a7880613a89939fd33d`
**Milestone SHAs:** MP24 closure `2425cc8`, MP24.1 `76ca228`, MP25 M1 `678c923` (+ docs `881c7dc`).

## 1. Stage Status Matrix

| Workstream | Status | Notes |
|---|---|---|
| MP23 Host Bridge HTTP auth & trust boundary | `IMPLEMENTED`, `TESTED`, `PUSHED` | `efee2c9` |
| MP24 A–H | `IMPLEMENTED`, `TESTED`, `PUSHED` | `2425cc8` |
| MP24 Criterion I (live TLS + devices) | `BLOCKED` | procedure written, not executed |
| MP24.1 revocation durability | `IMPLEMENTED`, `TESTED`, `PUSHED` | `76ca228` |
| MP25 assessment | `COMPLETED` (read-only) | gap matrix G-01…G-41 |
| MP25 M1 | `PARTIALLY COMPLETE` | repo scope verified; device/Xcode/TLS/keystore blocked |
| **MP26 M2** | **`IMPLEMENTED`, `TESTED` (repo) / `BLOCKED` (container E2E, CI-run inspection)** | verdict **M2 PARTIALLY COMPLETE** |

## 2. Architecture after M2

**Components (4 Go modules + 1 Flutter app):**

| Component | Path | Notes |
|---|---|---|
| Mobile (Android/iOS) | `apps/mobile` | unchanged in M2 |
| Host Bridge | `agents/host-bridge` | command policy + observability + health/metrics |
| AI Router | `agents/ai-router` | unchanged |
| ACP Adapter | `agents/acp-adapter` | unchanged |
| Connection Broker | `services/connection-broker` | canonical broker in `pkg/broker`, shared `pkg/observability` |

**Removed in M2:** `services/control-plane` (unused, unauthenticated), `services/connection-broker/internal/broker` (duplicate), `infrastructure/docker/Dockerfile.control-plane`.

## 3. M2 Security & Operations Changes

### 3.1 Command execution policy (`internal/security/command_policy.go`)
Single argument-aware policy used by both execution boundaries (direct HTTP and the agent
`execute_command` tool):
- Executable identity: bare allowlisted name or `./wrapper` only. Absolute paths, drive letters, UNC,
  sub-directory paths (`bin/git`) and traversal are rejected.
- Families: **git** (37 allowed sub-commands; `config`/`credential`/`alias`/`filter-branch`/`daemon`
  etc. rejected), **gradle** (project-defined tasks allowed; `-I/--init-script/-b/--build-file/-p/
  --project-dir/--include-build/--gradle-user-home/-c/--settings-file/--project-cache-dir` rejected),
  **adb** (inspection only; `shell/push/pull/install/root/remount/forward/reverse/tcpip/connect/
  logcat/reboot/sideload` rejected).
- Argument limits: 64 args, 4096 bytes/arg, UTF-8 validated, shell metacharacters and control
  characters rejected; errors never echo argument values.
- `ResolveExecutable` keeps workspace-relative executables inside the jail.
- `SanitizeEnvironment` strips loader/Git/JVM redirection variables before spawn.

### 3.2 Structured logging & metrics (`services/connection-broker/pkg/observability`)
- JSON-line logger: `kind=security|operational`, service/version/env, level filtering, **field-name and
  value-shape redaction** (`[REDACTED]`), truncation, composite summarisation, nil-safe, concurrency-safe.
- Metrics: dependency-free Prometheus text registry; strict low-cardinality names (invalid names are
  rejected and counted); counters + callback gauges.
- Wired security events: pairing success/failure (with reason codes), rate limiting,
  authentication/authorization denials, revocation success/failure, command-policy rejection,
  broker auth rejection, startup/shutdown.

### 3.3 Health & metrics endpoints
| Endpoint | Host Bridge | Broker | Protection |
|---|---|---|---|
| `/api/v1/health` | yes | yes | public (minimal) |
| `/api/v1/health/live` | yes | yes | public, no configuration disclosed |
| `/api/v1/health/ready` | 503 until security init completes | 503 if production secret missing | public |
| `/metrics` | host-admin bearer + confidential transport in prod | broker admin bearer | never public |

### 3.4 Deployment
- `Dockerfile.connection-broker`: Go 1.26 build (matches `go.mod`), `-trimpath -s -w`, non-root user,
  liveness `HEALTHCHECK`.
- `docker-compose.yml`: broker published on `127.0.0.1:8081` only, `DEVORBIT_BROKER_SECRET` required
  (fail-fast), read-only rootfs, `no-new-privileges`, tmpfs `/tmp`; PostgreSQL moved behind the
  `future-db` profile (unused until M4); control-plane service removed.
- `infrastructure/docker/.env.example` added; `infrastructure/deployment/Caddyfile` unchanged from M1
  (verified against `DEVORBIT_*` names).

## 4. Verified Verification Results (this session, after the final edit)

| Command | Result |
|---|---|
| `go vet ./...` + `go test -count=1 ./...` — host-bridge (14 packages) | **PASS** |
| `go vet ./...` + `go test -count=1 ./...` — connection-broker | **PASS** |
| `go test -tags=e2e -count=1 ./tests/e2e/` — real broker binary | **PASS** (2 scenarios, 9.5 s) |
| `go vet`/`go test` — ai-router, acp-adapter | **PASS** |
| `GOOS=linux`/`windows` `go build ./...` — host-bridge, broker, ai-router | **PASS** |
| `flutter analyze` | **PASS** — `No issues found!` |
| `flutter test` | **PASS** — `+49: All tests passed!` |
| Docker / container E2E locally | **BLOCKED** — daemon not running |
| GitHub Actions run inspection | **BLOCKED** — no `gh` CLI / authenticated API access |

## 5. Blockers

1. **Docker daemon not running on this host** (`npipe:////./pipe/dockerDesktopLinuxEngine` missing) →
   container-topology E2E, compose config validation and image build not executed locally. CI jobs
   (`deployment-validate`, `broker-binary-e2e`) cover them; results unverified.
2. **No authenticated GitHub access** → workflow run results for the pushed commit not inspected.
3. **M1 external dependencies unchanged:** physical Android/iOS devices, macOS/Xcode, CA-trusted TLS
   with DNS + proxy host, Android upload keystore + CI signing secrets, Linux `-race` execution.

## 6. Known gaps carried into M3+
- Task persistence/resumption, push streaming, change-set rollback (M3).
- Identity, database, account deletion, audit trail, compliance artefacts (M4).
- Usage/cost accounting, quotas, distributed rate limiting, broker registry/scale-out (M5).
- Host installers/auto-update, notifications, backups/DR, DPAPI blob cleanup (M6).
