# Agent Process Approval Flow

How an AI-requested Gradle command becomes an authorized execution, end to end.

## 1. Two approval mechanisms, deliberately separate

| Mechanism | Owner | Authorizes | Token form |
|---|---|---|---|
| Task approval (`ApprovalManager`) | agent engine | file edits, `git`/`adb` commands | `appr-…` |
| Process approval (`ProcessApprovalStore`) | process execution boundary | commands that run project-defined code (Gradle family) | proposal `pappr-…`, execution `pexec-…` |

They are not interchangeable. A task-approval token cannot authorize Gradle, and a process approval
cannot authorize a file edit. This is enforced in the host, not in the client.

## 2. Sequence

```
Mobile            Host (agent engine)              Host (approval boundary)
  │ POST /api/v1/agent/tasks
  │────────────────────────►
  │                          │ AI tool call: execute_command("./gradlew", ["assembleRelease"])
  │                          │ CommandPolicy.RequiresApproval → true
  │                          │ ProcessApprovalStore.Create(owner session/device, ws, cmd, args)
  │                          │   → state pending, command_digest, NO token
  │◄─────────────────────────│ task paused: approval_required, approval_token = "pappr-…"
  │ GET /api/v1/processes/approvals/{id}          (exact command/args/workspace/reason/expiry/digest)
  │──────────────────────────────────────────────►
  │ user inspects the dialog and chooses
  │ POST /api/v1/processes/approvals/{id}/decision {"decision":"approve","command_digest":"…"}
  │──────────────────────────────────────────────►  state approved, execution token minted (host-side)
  │ POST /api/v1/agent/tasks/approve {task_id, approval_token:"pappr-…"}
  │────────────────────────►  engine resumes the task
  │                          │ tool re-invokes execute_command with the approval id
  │                          │ ProcessService gate → ConsumeAuthorization(pappr-…)  [single use]
  │                          │ process launches
  │◄─────────────────────────│ task result (stdout/stderr/exit code) via polling
```

Rejection and cancellation never produce a token, so the task can never run the command.

## 3. Why the client carries the approval id, not the token

The agent flow resumes a task (`POST /api/v1/agent/tasks/approve`), and the resumed tool call needs
something to present. Rather than shipping an execution token through the agent, the task carries the
**proposal id**, which only works after a human decision moved the proposal to `approved`. The host
consumes it through `ProcessApprovalStore.ConsumeByApprovalID`, which revalidates owner, workspace,
command and arguments and is single-use. The execution token therefore never leaves the host on this
route.

## 4. Client responsibilities

`ProcessApprovalController` (shared with the direct HTTP flow) drives the UI:

1. `loadProposal(approvalId)` → `awaitingDecision`; `410` → `expired`; other failures → `failed`.
2. `approve()` → `decideProcessApproval` with the digest. If a resume hook is attached
   (`attachAgentTask`), the controller then resumes the task and reports `completed` **only** when the
   host confirms the resume; otherwise `failed`.
3. `reject()` → explicit `reject` decision; no resume is attempted.
4. `cancel()` → server-side cancel; `cancelled` only on host confirmation.

Guarantees encoded in the controller: no decision without a loaded proposal, duplicate taps ignored
while a request is in flight, network failures never imply approval or cancellation, and the execution
token is never exposed to the UI, logs or analytics.

## 5. Dialog requirements

`ProcessApprovalDialog` shows the executable, **every** argument (expandable, never silently
truncated), the workspace, the host's policy reason and the expiry. Approve is disabled for an expired
proposal. The approval id/digest are not rendered.

## 6. Host guarantees (unchanged by this flow)

- Proposal creation grants nothing.
- Only a decision carrying the proposal digest mints an authorization.
- Authorization is bound to session, device, workspace, command and argument vector.
- Consumption is single-use, expiry-bound and replay-resistant.
- Missing approval infrastructure fails closed on both routes.
- Cancellation is owner-bound and pending-only; a cancelled or rejected proposal cannot be approved.

## 7. Known limitations

- Process cancellation is confirmed by the host process runner, not by an OS process-tree reap.
- Expiry is evaluated when the client acts or the host consumes; there is no background push of expiry.
- Human presence versus a compromised client cannot be attested by the current client.
