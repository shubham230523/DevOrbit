# Latest Handoff

**Generated:** 2026-11-28
**Feature Branch:** `security/atomic-agent-task-execution`
**Target Remote Branch:** `quality/autonomous-15min-improvement`
**Verified Starting HEAD (MP26.2):** `2a3051968006695676e6925d5d623e4a5217f1c3`

## 1. Current Plan & Phase

- MP0–MP25, MP26 (M2), MP26.1: `COMPLETED` / `PARTIALLY COMPLETE` (external evidence blocked).
- **MP26.2: A–E and G implemented + tested locally; CI observation and container E2E `BLOCKED`.**
- M3 not started.

## 2. Last Completed Action

**Confirmed and fixed the MP26.1 self-approval flaw.** The old `POST /api/v1/processes/approvals`
returned a usable `approval_token` in the same response, so requesting approval *was* approval.

New protocol (implemented host + mobile):

1. `POST /api/v1/processes/approvals` → **202 pending**, `approval_id` + `command_digest`, **no token**.
2. `POST /api/v1/processes/approvals/{id}/decision` → `{"decision":"approve","command_digest":"…"}`
   → 200 with a single-use `execution_token`; `{"decision":"reject"}` → no token ever.
3. `POST /api/v1/processes/execute` with `execution_token` → revalidates session/device/workspace/
   command/args, consumes the token, then launches.
4. `GET /api/v1/processes/approvals/{id}` → status only (never the token).

Files: `internal/application/process_approval_store.go` (+test), `internal/transport/http/process_approval_handlers.go`,
`process_approval_e2e_test.go` (12 signed-HTTPS tests), `router.go`, `handlers.go` (token wipe + consume),
`cmd/host-bridge/main.go`, `docs/security/COMMAND_POLICY.md` §7;
mobile: `lib/features/tasks/application/process_approval_controller.dart` (+14 tests),
`lib/core/network/api_client.dart`, `lib/features/tasks/presentation/remote_tasks_screen.dart`.

Verification: host-bridge 14 packages PASS; store tests (incl. 64-goroutine consume race) PASS; 12
signed-HTTPS E2E PASS; broker/ai-router/acp-adapter PASS; linux+windows build PASS;
`flutter analyze` clean; `flutter test` **63/63**.

## 3. Exact Next Action

```bash
git push origin security/atomic-agent-task-execution
git push origin HEAD:quality/autonomous-15min-improvement
git ls-remote origin refs/heads/security/atomic-agent-task-execution refs/heads/quality/autonomous-15min-improvement refs/heads/main
```

Then (unchanged external blockers):
1. **CI activation** — Settings → Actions → General, Billing & plans → Actions minutes. No run has
   been created since 2026-11-23; every CI criterion remains `NOT RUN`.
2. **Container E2E** — start Docker Desktop, then `docker compose -f infrastructure/docker/docker-compose.yml config`
   and the `deployment-validate` smoke test.
3. **Linux symlink / Windows junction** runtime tests.
4. M3 work: task persistence/resumption, mobile push streaming.

## 4. What Must NOT Be Repeated

- Do not return an `execution_token` (or any usable capability) from the approval **request** endpoint.
- Do not merge propose and decide back into one call; do not let a decision omit the proposal digest.
- Do not make the token reusable, cross-session, or non-expiring; keep `wipeApprovalToken` before launch.
- Do not log tokens; the status endpoint must never include them.
- Do not weaken pairing proof/expiry/replay, HKDF, AES-256-GCM E2E, constant-time compares, header-only
  broker auth, durable revocation, fail-closed secret persistence, jail/symlink containment, the command
  policy, MP25 connection supervision, Android signing guards or cleartext restrictions.
- Do not modify `main`; do not force-push.

## 5. Security position (be precise)

The host can now establish that an execution was authorized by a **separate decision on the exact
proposal it issued**, which an AI-generated tool call cannot perform (agents hold no session and never
receive a usable token). What the host still cannot prove is that a *human* rather than a compromised
client performed the decision: that requires client-integrity attestation (platform capability), which
is documented as a residual risk, not claimed as solved.
