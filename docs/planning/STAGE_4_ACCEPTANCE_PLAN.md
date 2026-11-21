# DevOrbit Stage 4 — Acceptance Testing & Beta Readiness Plan

## Overview
Stage 4 establishes complete real-world acceptance, cross-platform verification, and beta readiness for DevOrbit across Flutter Android/iOS clients and Windows/Linux Go Host Bridge daemons.

## Verification Matrix & Test Strategy

### 1. Automated Test Baseline Execution
- Go Microservices & Host Bridge test suites (`go test ./...`)
- Flutter Mobile Application (`flutter analyze` & `flutter test`)

### 2. Host Bridge Runtime Verification (Windows & Linux)
- Daemon startup, graceful signal shutdown, configuration loader
- Workspace discovery, path jail containment, file reading (10 MB limit)
- SHA256 base-hash conflict detection & atomic temporary file write
- Controlled process runner with binary allowlisting (`git`, `gradlew`, `adb`) and argument sanitization
- Read-only Git status, branch detection, staged/unstaged/untracked files, `git diff`
- ADB device discovery (`adb devices -l`)
- Session revocation & rate-limited QR pairing handlers

### 3. Mobile Workspace Integration (Android & iOS)
- Connection Manager state machine & LAN/WSS mode switcher
- QR Pairing dialog & HMAC proof-of-possession solver
- Project explorer tree & breadcrumb path bar
- Remote Code Editor & SHA256 conflict resolution modal
- Remote tasks & live streaming stdout/stderr terminal log view
- Git workspace status & diff syntax inspector
- AI workspace with Google Antigravity ACP & Direct model provider adapters and human approval gate

### 4. Security & End-to-End Encryption Verification
- HKDF SHA-256 directional key derivation
- AES-256-GCM AAD frame binding
- Monotonic sequence replay attack rejection
- Connection Broker opaque frame routing (Non-decryption opacity)
- Operation request-id idempotency deduplication

### 5. Cross-Platform & Beta Packaging
- Android APK release build configuration
- iOS Xcode project configuration & Keychain access
- Linux cross-compilation & systemd unit setup
- Windows service wrapper setup
