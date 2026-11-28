# DevOrbit Agent Instructions

These instructions apply to every Antigravity, Gemini, or other coding-agent session working in this repository.

## 1. Mandatory Session Startup & Recovery Protocol

Before making any code or documentation change:

1. **Read `engineering/handoffs/latest-handoff.md` first.**
2. **Read `engineering/ACTIVE_PLAN.md` and `engineering/CURRENT_STATE.md` only as needed** for context on the active plan and verified state.
3. **Verify the actual Git state** before trusting any written summary:
   ```bash
   git status --short --branch
   git branch --show-current
   git rev-parse HEAD
   git log -5 --oneline --decorate
   git remote -v
   ```
   Also inspect the active feature branch and target remote branch heads via `git ls-remote origin`.
4. **Compare source code and Git against the checkpoint.** Treat Git and actual source code as authoritative over written claims. Do not discard uncommitted local changes; inspect remote divergence before proceeding.
5. **Resume from the first incomplete acceptance criterion.** Do not repeat completed phases or restart a repository-wide audit unless concrete evidence (regression, test failure, or code discrepancy) requires it.

## 2. Project Scope & Non-Negotiable Constraints

- **Active scope:** Android and iOS mobile clients (`apps/mobile`), Linux and Windows development hosts (`agents/host-bridge`), Connection Broker (`services/connection-broker`), Control Plane (`services/control-plane`), AI Router (`agents/ai-router`), and ACP Adapter (`agents/acp-adapter`).
- **Deferred / out of scope:** Web application, browser-based IDE, running Android Studio or emulators on mobile devices, and unrestricted remote shell access.
- **Connectivity:** Both local Wi-Fi / LAN and internet / broker-mediated remote access must remain supported.
- **AI integrations:** Official coding-agent integrations remain primary, with direct provider APIs as additional options.
- **Security & quality:** Never weaken authentication, authorization, workspace jailing, command allowlists, E2E encryption, or transport-confidentiality guarantees. Keep changes focused; avoid unrelated refactoring.
- **Secrets:** Never commit or write credentials, tokens, private keys, or secrets into documentation or handoffs.

## 3. Verification & Honesty Rules

- **Never assume a task is complete without evidence.**
- **Never invent test results, file paths, symbol names, commits, or verification outcomes.** Only report commands and tests you actually ran after your final edit and directly observed passing.
- Preserve existing tests and add regression tests for every new fix or security boundary change.

## 4. Checkpoint Protocol (Quota & Interruption Tolerance)

Update the continuity files after every meaningful phase and before ending a session:

1. `engineering/CURRENT_STATE.md`
2. `engineering/ACTIVE_PLAN.md`
3. `engineering/EXECUTION_LOG.md` (append-only)
4. `engineering/KNOWN_ISSUES.md` (when issues/risks change)
5. `engineering/handoffs/latest-handoff.md` (archive prior handoffs to `engineering/handoffs/archive/` when replacing major milestones)

Every checkpoint must record the exact commit SHA, commands actually run with results, the next single executable action, and distinguish:
- `IMPLEMENTED`
- `TESTED`
- `REVIEWED`
- `COMMITTED`
- `PUSHED`
- `REMOTE-VERIFIED`

## 5. Git Rules

- **Never modify `main`** or force-push any branch.
- Commit with descriptive messages and push both the active feature branch (`security/atomic-agent-task-execution`) and target remote branch (`quality/autonomous-15min-improvement`) when required by the active plan, verifying both remote heads match `HEAD`.
