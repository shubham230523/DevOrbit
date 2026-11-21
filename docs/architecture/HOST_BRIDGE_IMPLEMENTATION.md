# DevOrbit — Host Bridge Architecture & Implementation Specification

## 1. Clean Architecture Overview

The Host Bridge daemon follows Clean Architecture, isolating core domain entities from OS-specific filesystem and process execution infrastructure.

```
agents/host-bridge/
├── cmd/host-bridge/               # Entry point & HTTP server startup
├── internal/
│   ├── domain/                    # Entities (Workspace, FileEntry, ProcessSpec, GitStatus, ADBDevice)
│   ├── security/                  # WorkspaceJail, CommandPolicy (Path & Execution boundaries)
│   ├── infrastructure/
│   │   ├── filesystem/            # Atomic writes, SHA256 hashes, fsnotify file watcher
│   │   ├── process/               # Process execution runner, context timeouts, cancellation
│   │   ├── git/                   # Git CLI reader (status, diffs)
│   │   ├── adb/                   # ADB device scanner
│   │   └── discovery/             # Recursive project scanner & metadata builder
│   ├── application/               # Application Services (Workspace, Filesystem, Process, Git, Device)
│   ├── transport/http/            # REST API Handlers & HTTP Router
│   └── config/                    # Host Bridge environment configuration loader
```

## 2. Key Components

### Workspace Jail (`internal/security/sanitizer.go`)
- Maintains an explicit map of allowed workspace root paths.
- Canonicalizes input paths using `filepath.EvalSymlinks()` and `filepath.Clean()`.
- Validates that target paths remain strictly inside an allowed workspace root prefix before executing any filesystem or command operation.

### Conflict-Aware Storage Engine (`internal/infrastructure/filesystem/filesystem.go`)
- **Conflict Detection**: Compares incoming `base_sha256` against current host file SHA256 before writing. Returns HTTP 409 Conflict with host's current content and hash if modified.
- **Atomic Writes**: Writes new content to a hidden temporary file (`.devorbit_tmp_*`) in the target directory and renames it atomically to replace the destination file.
- **File Watching**: Uses `fsnotify` for recursive workspace directory event monitoring.

### Controlled Process Runner (`internal/infrastructure/process/runner.go`)
- Spawns executables using Go's `exec.CommandContext`.
- Validates binary names against an allowlist (`git`, `gradlew`, `adb`).
- Rejects shell injection characters (`;&|`$<>`).
- Enforces configurable execution timeouts (default: 60s, max: 300s) and supports immediate cancellation via process kill.
