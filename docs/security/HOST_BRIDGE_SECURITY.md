# DevOrbit — Host Bridge Security Enforcement Specification

## 1. Security Architecture Principles

The Host Bridge is designed with **Zero-Trust Client** principles. The host independently enforces security limits regardless of client state.

## 2. Threat Vector Mitigations

| Threat | Attack Vector | Mitigation in Host Bridge | Test Verification |
|---|---|---|---|
| **Path Traversal** | Requesting `../../Windows/System32` or symlink escaping root | `WorkspaceJail.ResolveAndValidate()` resolves symlinks via `filepath.EvalSymlinks()` and enforces prefix containment against workspace root. Rejects escapes with `403 Access Denied`. | `TestWorkspaceJail_PathTraversalAttacks` |
| **Arbitrary Code Execution** | Sending shell payload string (e.g. `cmd.exe /c rm -rf /`) | `CommandPolicy.ValidateCommand()` strictly enforces a binary allowlist (`git`, `gradlew`, `adb`) and rejects shell chaining operators (`;&\|`$<>`). | `TestCommandPolicy_Validation` |
| **Silent Data Overwrite** | Client writes stale code overwriting concurrent desktop edits | `StorageEngine.WriteFile()` compares client's `base_sha256` hash with host's current content hash. Rejects stale edits with `409 Conflict`. | `TestStorageEngine_ConflictDetectionAndAtomicWrite` |
| **Denial of Service (Disk/RAM)** | Reading multi-gigabyte binary files or running infinite process | Read file size capped at 10 MB (configurable). Process runner enforces context timeouts and process kill on cancellation. | `TestStorageEngine_ConflictDetectionAndAtomicWrite`, `TestProcessRunner_ExecutionAndCancellation` |
