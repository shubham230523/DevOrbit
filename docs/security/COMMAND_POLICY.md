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
approval proposal and only executes after the proposal's single-use token is consumed.

**HTTP path** (`/api/v1/processes/execute`): a Gradle command now requires an `approval_token`.

Two-step flow:

```http
POST /api/v1/processes/approvals
Authorization: Bearer <session token>
{"workspace_id": "...", "command": "gradlew", "args": ["assembleDebug"]}

200 {"approval_token": "appr-…", "reason": "...", "expires_at": "…"}
```

```http
POST /api/v1/processes/execute
{"workspace_id": "...", "command": "gradlew", "args": ["assembleDebug"],
 "approval_token": "appr-…"}
```

Token properties: bound to the requesting **session and device**, the **workspace**, the **exact
command and argument vector**; single-use; expires after 10 minutes; constant-time compared. A token
that is missing, expired, consumed, replayed, or presented by another session fails closed with
`403 approval_required`.

**Product impact:** clients that called `/api/v1/processes/execute` with a Gradle command in a single
step now receive `403 approval_required`. The mobile client must adopt the two-step flow (M3 UI work).
This is a deliberate fail-closed change, not an accident.

## 8. Related limits

- Maximum 64 arguments, 4096 bytes per argument.
- Arguments must be valid UTF-8 and may not contain `;` `&` `|` `` ` `` `$` `<` `>` or control
  characters (`NUL`, `LF`, `CR`).
- Errors never echo argument values, so rejected prompts cannot leak content into logs or responses.
