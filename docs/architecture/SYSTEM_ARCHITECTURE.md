# DevOrbit — System Architecture Specification

## 1. Executive Overview

DevOrbit is a secure, cross-platform mobile development workspace enabling Android and iOS users to remotely interact with their existing Android Studio project environments hosted on Linux and Windows workstations.

The architecture decouples lightweight mobile user interactions (code editing, AI prompt submission, build invocation, Logcat inspection) from resource-intensive host operations (Gradle compilation, Android emulator execution, static analysis, local Git operations).

```mermaid
graph TD
    subgraph Mobile Client [Flutter App (Android & iOS)]
        UI[Mobile UI: Code Editor / AI Workspace / Dashboard]
        MC[Mobile Connection Manager]
        KS[Keystore / Keychain Secrets]
    end

    subgraph Control Plane [Go Cloud Service]
        Auth[Auth & Session Service]
        Registry[Machine & Device Registry]
        DB[(PostgreSQL)]
    end

    subgraph Connection Broker [Go Relay Server]
        Broker[mTLS / WebSocket Connection Router]
    end

    subgraph Desktop Host [Linux / Windows Workstation]
        Bridge[Go Host Bridge Daemon]
        FS[Project Filesystem Allowlist]
        Git[Git CLI]
        Gradle[Gradle & Android SDK]
        ADB[ADB / Connected Android Devices]
        ACP[ACP / AI Agent Adapter]
    end

    UI --> MC
    MC -- HTTPS/REST --> Auth
    MC -- Local mTLS (LAN) --> Bridge
    MC -- Encrypted WS (Remote) --> Broker
    Bridge -- Outbound WS --> Broker
    Auth --> DB
    Registry --> DB
    Bridge --> FS
    Bridge --> Git
    Bridge --> Gradle
    Bridge --> ADB
    Bridge --> ACP
```

## 2. Core Architectural Components

### A. Mobile Application (`apps/mobile`)
- **Technology**: Flutter + Dart
- **Target Platforms**: Android (SDK 24+) and iOS (iOS 15+)
- **Responsibilities**:
  - Secure QR-code host pairing and mutual authentication key storage (Android Keystore / Apple Keychain).
  - Responsive multi-tab IDE interface (Projects, Code Editor, AI Workspace, Terminal, Machines, Settings).
  - High-performance syntax highlighting code editor with conflict detection and side-by-side diffing.
  - Streaming terminal and build log renderer.

### B. Control Plane (`services/control-plane`)
- **Technology**: Go (Clean Architecture) + PostgreSQL
- **Responsibilities**:
  - User authentication (JWT tokens, OAuth2 device flows).
  - Machine registration and authorization token issuance.
  - Security audit logging and session lifecycle enforcement.

### C. Connection Broker (`services/connection-broker`)
- **Technology**: Go + WebSockets
- **Responsibilities**:
  - Secure tunnel routing between mobile clients and desktop host bridges when off-LAN.
  - Outbound-only host bridge connections (no open inbound public ports required on host).
  - Heartbeat monitoring and automatic session reconnection.

### D. Host Bridge (`agents/host-bridge`)
- **Technology**: Go (Linux systemd service & Windows Service/Background Agent)
- **Responsibilities**:
  - Auto-discovery of local Android Studio workspaces and `.gradle` projects.
  - Filesystem jail enforcement: Path validation against explicit allowed workspace roots.
  - Controlled execution of Git CLI, Gradle builds, and ADB device inspection commands.
  - Streaming file change events and command outputs over WebSocket.

### E. AI Ecosystem (`agents/acp-adapter` & `agents/ai-router`)
- **Technology**: Go
- **Responsibilities**:
  - Agent Client Protocol (ACP) adapter for official agent integrations (Google Antigravity, local ACP agents).
  - Provider adapters for direct LLM API access (Gemini API, OpenAI, Anthropic).
  - Tool execution security middleware (requires explicit mobile user confirmation for file writes and shell execution).

## 3. Communication Protocols

| Channel | Protocol | Security Baseline |
|---|---|---|
| Mobile <-> Control Plane | HTTPS / REST | TLS 1.3, JWT Bearer Auth |
| Mobile <-> Host (LAN) | mTLS / WebSockets | Mutual TLS with TLS 1.3 & pinned certs |
| Host Bridge -> Connection Broker | Outbound WSS | TLS 1.3 + Host Token Auth |
| Mobile <-> Connection Broker | Outbound WSS | TLS 1.3 + End-to-End Encrypted Tunnel |
| Host Bridge <-> ACP Agent | JSON-RPC over Stdio/Unix Socket | Local Process Isolation |

## 4. Trust & Security Boundaries

1. **Mobile Device**: Trusted user endpoint. Stores private key material in hardware security module.
2. **Control Plane**: Trusted registry. Has ZERO direct access to host filesystem or code contents.
3. **Connection Broker**: Untrusted relay server. Transmits E2E encrypted streams; cannot inspect code or shell payloads.
4. **Desktop Host Bridge**: Highest security boundary. Enforces strict path allowlists, process sandbox policies, and user approval flags.
