# ADR-0001: Architectural Baseline — Flutter Mobile & Go Host Bridge

- **Status**: Accepted
- **Date**: 2026-11-21
- **Deciders**: Principal Architect, Mobile Lead, Backend Lead

## Context
DevOrbit requires a responsive, high-performance cross-platform mobile frontend (Android and iOS) capable of syntax highlighting, real-time streaming terminal interaction, and AI assistant operations. Simultaneously, a lightweight, cross-platform host daemon (Linux and Windows) is required to perform system-level file operations, Git operations, process execution, and ADB device inspection.

## Decision
1. **Mobile Frontend**: Build the client application using Flutter + Dart. Flutter provides unified native UI compilation for iOS and Android, rich widget eco-system, and excellent WebSocket streaming performance.
2. **Host Bridge & Cloud Backend Services**: Implement all host-side daemons, connection brokers, and control plane microservices in Go. Go provides cross-platform compilation (single static binaries for Linux and Windows), minimal memory footprint, high concurrency goroutine model for streaming WSS, and zero heavy runtime dependencies.
3. **No Web App Modules**: DevOrbit explicitly targets Android and iOS native mobile clients. Web application modules are excluded from the initial product scope to avoid compromising mobile UX performance.

## Consequences
- Single Flutter codebase serves both Android and iOS mobile platforms.
- Single Go codebase compiles natively for Linux systemd services and Windows background daemons.
- Strong type safety across APIs via OpenAPI contract generation.
