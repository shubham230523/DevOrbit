# Command Execution Policy

This document describes how the DevOrbit Host Bridge authorizes command execution. It is the
authoritative description of `agents/host-bridge/internal/security/command_policy.go` and
`internal/infrastructure/process/runner.go`.

Both execution boundaries use this one policy:

1. `POST /api/v1/processes/execute` (authenticated mobile session).
2. The agent `execute_command` tool (AI-generated tool call).

## 1. Trust model

| Input | Trust |
|---|---|
| Command name and argument vector | **Untrusted** — may originate from AI output, repository content, or prompt injection |
| Repository build scripts (`build.gradle`, `settings.gradle`) | **Untrusted** for authorization purposes: executing any Gradle task runs project-supplied code |
| Environment inherited from the Host Bridge process | Untrusted (sanitised before spawn) |
| Session permissions | Trusted (established at pairing, revocable) |

## 2. Allowed executables

Only these base names are accepted: `git`, `gradle`, `gradlew`, `adb` (plus their `.exe`/`.bat`
variants), and the workspace-relative wrapper form `./gradlew` / `.\gradlew.bat`.

Rejected:

- Absolute paths (`/usr/bin/git`, `C:\Windows\git.exe`, `C:\git.exe`).
- UNC paths (`\\server\share\git.exe`).
- Sub-directory paths (`bin/git`) — a repository could ship a binary that merely shares an allowlisted
  name.
- Traversal (`./../git`, `./sub/../../git`).
- Unresolvable, dangling, non-regular, or escaping symlink targets (see §5).

## 3. Argument rules (git)

Options are parsed structurally, not by whole-token matching, so these are all rejected:

| Form | Example |
|---|---|
| Separate value | `git -c core.pager=sh status` |
| Attached value | `git -ccore.pager=sh status` |
| Long with `=` | `git --config-env=core.pager=E status` |
| Long with space | `git --git-dir /tmp/evil status` |
| Cluster | `git -Cf status` |

Denied short options: `-c`, `-C`.
Denied long options: `--config-env`, `--git-dir`, `--work-tree`, `--namespace`, `--exec`,
`--exec-path`, `--upload-pack`, `--receive-pack`, `--open-files-in-pager`, `--ext-diff`,
`--textconv`, `--no-index`, `--output`.

Denied sub-commands (not in the allowlist): `config`, `credential`, `alias`, `help`, `daemon`,
`filter-branch`, `instaweb`, `upload-pack`, `receive-pack`, `svn`, `shell`, `http-backend`,
`bisect`, `submodule`, `difftool`, `mergetool`.

Allowed sub-commands include inspection (`status`, `diff`, `log`, `show`, `branch`, `rev-parse`,
`rev-list`, `ls-files`, `ls-tree`, `blame`, `describe`, `shortlog`, `grep`, `version`, `remote`,
`tag`), working-tree mutation (`add`, `restore`, `checkout`, `switch`, `stash`, `commit`, `reset`,
`clean`, `apply`, `rm`, `mv`) and synchronization (`fetch`, `pull`, `push`, `merge`).

Matching is case-sensitive: `-C` (directory) is not `-c` (config), and Gradle `-I` (init script) is
not `-i` (info). When a cluster contains an option that consumes a value, scanning stops, so a commit
message containing `-c` is not mistaken for an option.

## 4. Argument rules (Gradle and adb)

**Gradle** — any task name is accepted (tasks are project-defined), but every option that redirects
script, build, settings, project or cache resolution is rejected in all spellings:

- Short (separate or attached): `-I`, `-b`, `-c`, `-p`, `-g`.
- Long (with `=` or space): `--init-script`, `--build-file`, `--settings-file`, `--project-dir`,
  `--include-build`, `--gradle-user-home`, `--project-cache-dir`.

`-Pkey=value` and `-Dkey=value` remain available because they set properties, not script sources.

**adb** — inspection only: `devices`, `version`, `get-state`, `wait-for-device`, `start-server`,
`kill-server`. Rejected: `shell`, `push`, `pull`, `install`, `uninstall`, `root`, `remount`,
`exec-out`, `forward`, `reverse`, `tcpip`, `connect`, `logcat`, `reboot`, `sideload`.
`-s <serial>`, `-P`, `-H` are accepted with a validated serial/host value.

## 5. Executable containment

`ResolveExecutable` performs **real-filesystem** containment, not a lexical check:

1. The lexical path must stay inside the workspace root.
2. The workspace root is canonicalized (`EvalSymlinks`).
3. The target must resolve (`EvalSymlinks`); dangling links fail closed.
4. The **resolved** path must remain inside the canonicalized root, so nested links cannot escape.
5. The final target must be a regular file.

The resolved absolute path is what gets executed.

**Residual risk (documented, not eliminated):** validation and execution are separate operations, so
a writable workspace can swap the file in between (TOCTOU). Eliminating the window requires OS
primitives (`openat`/`O_NOFOLLOW`) that are platform-specific. Git can also execute diff drivers
declared in a repository's own `.git/config`; the policy blocks the option and environment routes to
that behaviour (`--ext-diff`, `--textconv`, `GIT_EXTERNAL_DIFF`, `GIT_PAGER`), but a repository the
user has already trusted remains a code-execution surface — as it does for any build system.

## 6. Environment sanitisation

Before spawn, these are removed from the inherited environment: `LD_PRELOAD`, `LD_LIBRARY_PATH`,
`LD_AUDIT`, `DYLD_*`, `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`,
`GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_ASKPASS`, `SSH_ASKPASS`,
`GIT_CONFIG*`, `GIT_CONFIG_KEY_*`, `GIT_CONFIG_VALUE_*`, `GIT_EXTERNAL_DIFF`, `GIT_PAGER`, `PAGER`,
`GIT_EDITOR`, `GIT_SEQUENCE_EDITOR`, `GIT_TEMPLATE_DIR`, `BASH_ENV`, `ENV`, `PS4`, `PROMPT_COMMAND`,
`JAVA_TOOL_OPTIONS`, `_JAVA_OPTIONS`, `JDK_JAVA_OPTIONS`, `GRADLE_OPTS`, `NODE_OPTIONS`,
`PYTHONSTARTUP`, `PERL5OPT`.

## 7. Gradle execution requires explicit approval

Argument validation cannot make Gradle safe: **every** task, including `help`, evaluates
project-defined build scripts. Gradle is therefore a separate trust boundary and requires an explicit,
auditable authorization in addition to session permission.

**Agent path** (`execute_command` tool): unchanged and unconditional — the tool always returns an
approval proposal and only executes after the proposal's single-use token is consumed by the host
when the task is resumed from `/api/v1/agent/tasks/approve`. The AI never receives a token it can use.

**HTTP path** (`/api/v1/processes/execute`): Gradle requires an `execution_token` issued only by an
explicit decision on a pending proposal.

Requesting an approval grants **nothing**. The earlier MP26.1 endpoint returned a usable token in the
same response, which meant request and grant were the same operation; that is fixed.

### Step 1 — propose (no capability issued)

```http
POST /api/v1/processes/approvals
Authorization: Bearer <session token>
{"workspace_id": "...", "command": "./gradlew", "args": ["assembleDebug"]}

202 {"approval_id": "pappr-…", "state": "pending",
     "workspace_id": "...", "command": "./gradlew", "args": ["assembleDebug"],
     "reason": "gradle executes project-defined build logic",
     "command_digest": "<sha256 over the canonical request>",
     "expires_at": "…"}
```

The response deliberately contains **no** `execution_token`. A client that only knows how to request
an approval can never execute.

### Step 2 — decide (the human action)

```http
POST /api/v1/processes/approvals/{approval_id}/decision
{"decision": "approve", "command_digest": "<digest from step 1>"}

200 {"approval_id": "pappr-…", "state": "approved",
     "execution_token": "pexec-…", "expires_at": "…"}
```

An approval must echo the digest of the proposal the user was shown, so a decision can only apply to
the exact command the host issued. A rejection needs no digest and never produces a token:

```http
{"decision": "reject"}  ->  200 {"state": "rejected"}
```

### Step 3 — execute

```http
POST /api/v1/processes/execute
{"workspace_id": "...", "command": "./gradlew", "args": ["assembleDebug"],
 "execution_token": "pexec-…"}
```

The host revalidates session, device, workspace, command and argument vector immediately before
launch and deletes the token, so one approval authorizes exactly one execution.

### Status

```http
GET /api/v1/processes/approvals/{approval_id}
200 {"approval_id": "…", "state": "pending|approved|rejected|expired|consumed", ...}
```

The status representation never contains the execution token.

### Error behaviour (all fail closed)

| Condition | Status |
|---|---|
| Missing token on a Gradle execution | 403 `approval_required: approval_missing` |
| Unknown/already-consumed/replayed token | 403 `approval_required: token_not_found_or_consumed` |
| Proposal unknown | 403 `approval_required: proposal_not_found` |
| Proposal already decided | 409 `approval_required: proposal_already_decided` |
| Proposal expired | 410 `approval_required: proposal_expired` |
| Decision digest mismatch | 403 `approval_required: digest_mismatch` |
| Decision/consumption by another session or device | 403 `approval_required: owner_mismatch` |
| Execution does not match the approved workspace/command/args | 403 `approval_required: scope_mismatch` |
| Command needs no approval | 400 on the approval endpoints |

### Token properties

Bound to the requesting **session and device**, the **workspace**, the **exact command and argument
vector**; single-use; expires after 10 minutes (same window as the proposal); constant-time compared;
removed from the request before the process starts, so it is never logged, echoed, or inherited by the
child process.

### Mobile flow

`RemoteTasksScreen` drives `ProcessApprovalController`:

1. Propose → a dialog shows the exact command, arguments, workspace, reason and expiry.
2. **Approve** → the decision is submitted; execution is attempted only after the host returns a token.
3. **Reject** → nothing is executed and no token exists.
4. **Cancel** → the proposal simply expires host-side; nothing is authorized locally.

States rendered: `requesting`, `awaitingDecision`, `approved`, `executing`, `completed`, `rejected`,
`cancelled`, `expired`, `unavailable`, `failed`. Duplicate taps are ignored while a request is in
flight, and a network failure never auto-retries an approval.

**Product impact:** a single-step Gradle execution now returns `403 approval_required`. This is a
deliberate fail-closed change; the client implements the two-step protocol above.

## 8. Related limits

- Maximum 64 arguments, 4096 bytes per argument.
- Arguments must be valid UTF-8 and may not contain `;` `&` `|` `` ` `` `$` `<` `>` or control
  characters (`NUL`, `LF`, `CR`).
- Errors never echo argument values, so rejected prompts cannot leak content into logs or responses.
