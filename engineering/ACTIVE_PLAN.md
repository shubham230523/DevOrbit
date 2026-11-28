# DevOrbit — Active Plan

## Completed Plans

### Master Plan 0 / 0.1 — Persistent Engineering Memory & Cross-Session Continuity

- **Status:** `COMPLETED` (`COMMITTED`, `PUSHED`, `REMOTE-VERIFIED`: MP0 at `7aed09683a829f83dd3a6f04cfbba0f6e376bc31`, MP0.1 at `be4ec8aa93cdfb8463f393f9f5d1ee0d457ef60d`).

### Master Plan 23 — Host Bridge HTTP Auth & Trust Boundary

- **Status:** `IMPLEMENTED`, `TESTED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` at `efee2c99e16427223c803b89460dbe61fd151064`.

### Master Plan 24 — Pairing Bootstrap, Host-Admin Confidentiality & Closure Remediation

- **Status:** Criteria A–H `IMPLEMENTED`/`TESTED`/`REVIEWED`/`COMMITTED`/`PUSHED` at `2425cc838c64e7e1e28c67e33c1636d45e5889d7`.
- **Criterion I (live TLS + physical mobile validation):** `BLOCKED` (external dependencies). Procedure now written: `docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md`.

### Master Plan 24.1 — Revocation Durability & Rollback Failure Closure

- **Status:** `IMPLEMENTED`, `TESTED`, `REVIEWED`, `COMMITTED`, `PUSHED`, `REMOTE-VERIFIED` at `76ca2286523b1978acec1e64723d74ec64cddf2d`.
- **Accepted residuals:** DPAPI orphaned sealed-blob cleanup, legacy `pkg/broker` duplicate (see `KNOWN_ISSUES.md`).

### Master Plan 25 — Production Readiness Assessment

- **Status:** `COMPLETED` (read-only assessment; `DEVORBIT_PRODUCTION_READINESS_MASTER_ASSESSMENT.md`, 17 parts, gap matrix G-01…G-41).

## Active Master Plan

### Master Plan 25 · M1 — Verification Closure, Release Build Integrity & Store-Critical Platform Config

- **Verified Starting HEAD:** `6e93ea43d815182bddcc6497df90c16f8aebcbda`
- **Objective:** convert assessed `PARTIALLY IMPLEMENTED` items into verified repository implementations for connectivity truthfulness, release signing, store-critical platform config, CI verification depth, and TLS deployment prerequisites.
- **Overall Status:** repository scope `IMPLEMENTED` + `TESTED`; external-evidence scope `BLOCKED`. **Verdict: M1 PARTIALLY COMPLETE.**

| Task | Scope | Status | Evidence |
|---|---|---|---|
| A | Real transport-derived connection state (G-12) | `IMPLEMENTED`, `TESTED` | `connection_manager.dart` rewrite, `broker_websocket_client.dart` hardening, dashboard wiring, 12 new real-WebSocket tests |
| B | Android production release configuration (G-H3/G-28) | `IMPLEMENTED`, `TESTED`, `VERIFIED` (local builds) | `app/build.gradle.kts` signing + fail-closed guard, manifest/backup/network-security config, `KEYSTORE_SETUP.md`; debug APK built, release refused without creds, release built with ephemeral keystore |
| C | iOS store-critical configuration (G-29) | `IMPLEMENTED`; compile/TestFlight `BLOCKED` | `Info.plist` (local network + ATS), `IOS_STORE_READINESS.md` |
| D | CI/CD verification depth (G-27) | `IMPLEMENTED` (workflow); race detector `BLOCKED` locally, CI job added | `.github/workflows/ci.yml` rewrite |
| E | Live TLS deployment prerequisites (G-10) | `IMPLEMENTED` (repo); live validation `BLOCKED` | `Caddyfile`, `env.example`, deployment README, validation procedure |
| F | Hygiene + continuity | `IMPLEMENTED` | No tracked binaries/secrets; empty `tests/` removed; continuity docs updated |

#### Remaining external dependencies (blocking full M1)

1. Production Android upload keystore + 4 CI secrets (`KEYSTORE_SETUP.md`).
2. Physical Android device (pairing/E2E/revocation evidence).
3. macOS + Xcode + Apple account (iOS compile/TestFlight/App Review).
4. Physical iPhone.
5. DNS name + reachable proxy host + Caddy for CA-trusted TLS validation.
6. Linux 64-bit CGO runner (`go test -race` executes in CI once pushed).

## Exact Next Engineering Action

1. Push both engineering branches and confirm identical remote heads (`git ls-remote`).
2. Configure the four Android signing secrets in GitHub and confirm the signed-release job produces a non-debug-signed AAB artifact.
3. On a host with DNS + Caddy, execute `docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md` Steps 1–6 and record evidence.
4. On macOS, run `flutter build ios --release --no-codesign` and record the result.
5. Then begin M2 (security hardening & operations baseline) per the MP25 roadmap.
