# DevOrbit Stage 3 — Implementation & Verification Plan

## Overview
Stage 3 delivers the complete mobile workspace and remote IDE experience for Android and iOS clients connecting to Windows and Linux development host machines over direct LAN mTLS and E2E encrypted Connection Broker tunnels.

## Target Architecture & Modules

### 1. Presentation Layer (`apps/mobile/lib/features/`)
- **Dashboard (`features/dashboard/`)**: Connected machine health, LAN/Relay mode switcher, latency meter, active machine revocation and disconnect actions.
- **Projects & File Explorer (`features/projects/`)**: Workspace discovery API client, directory tree navigator, breadcrumb path bar, file search, entry metadata.
- **Code Editor (`features/editor/`)**: Syntax highlighting for 12+ languages, line numbering, find in file, save with SHA256 conflict detection & 2-way/3-way diff conflict resolution modal.
- **Tasks & Execution (`features/tasks/`)**: Gradle builds, tests, Go tests, Git commands, live stdout/stderr streaming log terminal with auto-scroll and pause, process cancellation.
- **Git Workspace (`features/git/`)**: Branch indicator, staged/unstaged/untracked file status tree, side-by-side diff viewer, commit log viewer.
- **AI Workspace (`features/ai_workspace/`)**: Antigravity ACP & Direct model prompt interface, streaming response, tool execution proposal with diff preview, explicit human approval gate.
- **Settings (`features/settings/`)**: Biometrics preference, AI tool approval flag, security audit log inspector.

### 2. Network & Application Services Layer (`apps/mobile/lib/core/`)
- `network/api_client.dart`: HTTP/REST & WebSocket RPC client communicating with Host Bridge endpoints (`/api/v1/*`).
- `network/connection_manager.dart`: Connection state machine (`disconnected`, `connecting`, `connected`, `reconnecting`, `failed`, `revoked`).
- `security/secure_storage.dart`: Hardware KeyStore/Keychain token & session key store.

### 3. Backend & Host Bridge Extensions (`agents/host-bridge/`)
- Integration endpoints for workspace listing, file operations, process execution with request ID idempotency, Git status/diffs, and ADB device listing.

## Verification Matrix
- **Unit & Widget Tests**: Flutter BLoC/StateNotifier tests, widget interaction tests, editor conflict tests.
- **Go Microservices Tests**: Security tests, path jail enforcement, HKDF directional key tests, process execution tests.
- **Security & Regression**: Verification of HKDF directional keys, AES-256-GCM AAD frame binding, monotonic sequence replay rejection, pairing lockout, and device revocation.
