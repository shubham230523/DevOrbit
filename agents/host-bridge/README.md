# DevOrbit Host Bridge Daemon

The DevOrbit Host Bridge is a lightweight, high-performance Go daemon running on developer workstation computers (Linux & Windows). It acts as the secure interface between mobile clients (Android & iOS) and local development host environments.

## Core Capabilities

- **Workspace Discovery**: Auto-detects Android Studio projects, Gradle multi-module projects, and Git repositories under configured search roots.
- **Secure Filesystem Engine**: Directory listing, file reading (10MB limit), conflict-aware file writes (with SHA256 base hash verification and atomic temporary file replacement), and recursive file change watching (`fsnotify`).
- **Controlled Process Execution**: Spawns allowlisted binaries (`git`, `gradlew`, `./gradlew`, `gradle.bat`, `adb`) with context cancellation, execution timeouts, and background streaming. Prohibits arbitrary shell string execution.
- **Git Repository Inspection**: Read-only Git status, branch detection, staged/unstaged file listing, and diff text retrieval.
- **Android ADB Device Discovery**: Lists connected physical and emulated Android devices (`device`, `unauthorized`, `offline`) and device metadata.
- **REST / OpenAPI HTTP Server**: Exposes versioned endpoints conforming to `packages/api-contracts/openapi.yaml`.

## Running the Host Bridge

```bash
# Build binary
go build -o bin/host-bridge ./cmd/host-bridge

# Run Host Bridge (default port: 8082)
./bin/host-bridge
```

## Configuration (Environment Variables)

- `DEVORBIT_PORT`: HTTP API listening port (default: `8082`).
- `DEVORBIT_MAX_FILE_SIZE_MB`: Maximum file size for reading (default: `10` MB).

## Running Tests

```bash
go test -v ./...
```
