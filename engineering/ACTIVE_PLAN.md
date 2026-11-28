# DevOrbit — Active Plan

## Completed Plans

- **Master Plan 0 / 0.1 (continuity):** `COMPLETED` (`7aed096`, `be4ec8a`).
- **Master Plan 23 (Host Bridge HTTP auth & trust boundary):** `IMPLEMENTED`, `TESTED`, `PUSHED` (`efee2c9`).
- **Master Plan 24 (pairing bootstrap & closure remediation):** Criteria A–H `IMPLEMENTED`/`TESTED`/`PUSHED` (`2425cc8`); **Criterion I live TLS + physical devices `BLOCKED`** (procedure: `docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md`).
- **Master Plan 24.1 (revocation durability & rollback closure):** `IMPLEMENTED`, `TESTED`, `PUSHED` (`76ca228`).
- **Master Plan 25 (production-readiness assessment):** `COMPLETED` (read-only, gap matrix G-01…G-41).
- **Master Plan 25 · M1 (verification closure & release integrity):** `PARTIALLY COMPLETE` (`678c923`, `881c7dc`). Repository scope implemented and verified; device/Xcode/TLS/keystore evidence blocked.

## Active Master Plan

### Master Plan 26 · M2 — Security Hardening & Operations Baseline

- **Verified Starting HEAD:** `881c7dce958ffd2723ba4a7880613a89939fd33d`
- **Objective:** remove accidentally deployable unauthenticated surfaces, centralise command authorization, and establish the logging/metrics/health/deployment/verification baseline without redesigning working subsystems.
- **Overall Status:** repository scope `IMPLEMENTED` + `TESTED`; container-topology E2E and CI-run inspection `BLOCKED`. **Verdict: M2 PARTIALLY COMPLETE.**

| Task | Scope | Status | Evidence |
|---|---|---|---|
| A | Control Plane disposition | `IMPLEMENTED`, `VERIFIED` (removal) | module + Dockerfile deleted, compose/CI/docs updated; zero external imports |
| B | Legacy broker disposition | `IMPLEMENTED`, `TESTED` | canonical `pkg/broker` only; `internal/broker` deleted; E2E aligned to v1.2 control frames |
| C | Per-command security policy | `IMPLEMENTED`, `TESTED` | `command_policy.go` + 12 test functions (git/gradle/adb/paths/env/injection/oversize) |
| D | Structured logging & redaction | `IMPLEMENTED`, `TESTED` | `pkg/observability` logger; redaction + concurrency + level tests |
| E | Metrics & health | `IMPLEMENTED`, `TESTED` | counters/gauges, `/metrics` admin-protected, live/ready semantics; contract tests assert them |
| F | Deployment hardening | `IMPLEMENTED`, `VALIDATED` (compose parse locally) | non-root image, loopback-only publish, fail-fast secret, profiles, healthchecks |
| G | Executable/container E2E | `IMPLEMENTED` (real-binary), **`BLOCKED`** (Docker) | `tests/e2e` (tag `e2e`) passes locally; container scenarios run in CI only |
| H | Contract tests | `IMPLEMENTED`, `TESTED` | 16 protected + 3 admin routes, error envelope, no-secret responses, readiness |
| I | CI verification | `IMPLEMENTED` (workflow) / **`BLOCKED`** (run inspection) | matrices updated, new jobs added; no `gh` CLI, no Docker daemon locally |

#### Blockers for full M2 closure

1. **Docker daemon unavailable** on the implementation host → container-topology E2E and compose/image validation not executed locally (CI jobs added: `deployment-validate`, `broker-binary-e2e`).
2. **No GitHub CLI / authenticated API access** → actual workflow run results for the pushed commit not inspected.
3. Recurring external dependencies from M1 remain (physical devices, macOS/Xcode, CA-trusted TLS, upload keystore).

## Exact Next Engineering Action

1. Push both engineering branches and confirm identical remote heads.
2. Open the Actions run for the pushed commit (GitHub UI or `gh run watch`) and record: `go-test`, `go-race`, `go-cross-build`, `flutter-analyze-test`, `android-debug-build`, `broker-binary-e2e`, `deployment-validate`, `secret-scan`, `dependency-scan`. Close Task I on evidence.
3. Execute `docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md` on a host with DNS + Caddy + devices.
4. Then begin M3 (execution completeness) per the MP25 roadmap.
