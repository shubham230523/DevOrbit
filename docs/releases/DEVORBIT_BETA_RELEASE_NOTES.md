# DevOrbit v1.0.0-beta Release Notes

Welcome to the initial Beta Release of **DevOrbit** — the cross-platform mobile workspace enabling developers to access their desktop Android Studio development host computers from Android and iOS devices.

## Features Included in Beta

1. **Dual-Mode Connectivity**: Direct LAN mutual TLS (mTLS) for ultra-low latency on local Wi-Fi, and Internet Relay via the Connection Broker with End-to-End Encryption (AES-256-GCM) when remote.
2. **QR Device Pairing**: Single-use QR code pairing challenge with HMAC-SHA256 proof-of-possession verification and rate-limiting brute force protection.
3. **Workspace Discovery & Explorer**: Auto-discovers Android Studio, Gradle, and Git projects with breadcrumb navigation and file search.
4. **Remote Code Editor**: Syntax highlighting for 12+ development languages, line numbering, and SHA256 base-hash conflict detection with a 2-way/3-way conflict resolution modal.
5. **Remote Tasks & Streaming Log Terminal**: One-tap Gradle builds, unit tests, and ADB device scans with real-time stdout/stderr streaming logs and process cancellation.
6. **Git Workspace Integration**: Displays branch status, staged/unstaged/untracked file status tree, and `git diff` syntax viewer.
7. **AI Workspace & Human Approval Gate**: Integrates Google Antigravity ACP agents and direct model APIs with an interactive Tool Call Proposal card requiring explicit human confirmation before applying host file edits or commands.

## Installation Instructions

### Host Workstation (Windows / Linux)
```bash
# Build Host Bridge binary
cd agents/host-bridge
go build -o bin/host-bridge ./cmd/host-bridge

# Run Host Bridge
./bin/host-bridge
```

### Mobile Application (Android / iOS)
```bash
cd apps/mobile
flutter pub get
flutter run --release
```
