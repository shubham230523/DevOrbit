# ADR-0003: Host Bridge Filesystem Jail & Process Execution Security

- **Status**: Accepted
- **Date**: 2026-11-21
- **Deciders**: Principal Architect, Security Lead

## Context
The Host Bridge daemon runs on the user's host workstation with user privileges. A compromised or buggy remote connection could attempt path traversal attacks or execute arbitrary malicious shell commands.

## Decision
1. **Explicit Workspace Allowlist**: The Host Bridge must reject any filesystem access outside explicitly designated project root directories.
2. **Canonical Path Validation**: Path resolution must clean and resolve all symbolic links using `filepath.EvalSymlinks()` and verify that the canonical path resides within an allowed workspace boundary.
3. **Strict Binary Allowlist for Execution**: Arbitrary shell invocation (e.g. `cmd.exe /c` or `bash -c`) is prohibited. Only pre-configured executable binaries (`git`, `gradlew`, `gradle.bat`, `adb`) with parsed argument arrays may be spawned.
4. **Mutual TLS & Certificate Pinning**: Host Bridge pairing generates device-specific certificates. Mobile clients pin host certificates during QR code pairing.

## Consequences
- Prevents remote path traversal threats (e.g., accessing SSH keys or system credentials).
- Mitigates command injection vulnerabilities by prohibiting arbitrary shell syntax execution.
