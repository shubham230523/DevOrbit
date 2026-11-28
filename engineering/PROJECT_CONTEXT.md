# DevOrbit — Project Context

## 1. Purpose & Supported Platforms

DevOrbit is a mobile-first remote development workspace that lets developers inspect, edit, build, test, and run AI-assisted workflows against real projects hosted on their development workstation.

**Core philosophy:** *The development computer does the work; the mobile device provides the secure interface.*

| Area | Supported Scope |
|---|---|
| Mobile clients | Android, iOS (`apps/mobile`) |
| Development hosts | Linux, Windows (`agents/host-bridge`) |
| Connectivity | Direct local Wi-Fi / LAN and internet broker relay |
| Source control | Git and GitHub |
| AI integrations | Official agent integrations (primary, e.g., ACP) and direct model APIs (Gemini, etc.) |

### Explicitly Deferred / Out of Scope

- Web application and browser-based IDE.
- Running Android Studio, Gradle builds, or Android emulators directly on mobile devices.
- Replacing desktop IDEs.
- Unrestricted arbitrary remote shell execution.

## 2. Architecture & Module Responsibilities

- **`agents/host-bridge` (Go):** Runs on the developer's Linux/Windows workstation. Enforces workspace jailing, command allowlisting, pairing bootstrap, session store & permissions, direct E2E HTTP tunnel (`/api/v1/e2e`), outbound broker WebSocket client, filesystem/Git/process/ADB services, and agent task execution.
- **`services/connection-broker` (Go):** Internet relay connecting paired mobile WebSocket clients (`/ws/client`) to host bridge outbound WebSocket sessions (`/ws/host`), enforcing session authorization and revocation.
- **`services/control-plane` (Go):** Backend control-plane service for pairing/device metadata and auth workflows.
- **`agents/ai-router` (Go):** Provider-agnostic AI routing layer (e.g., Gemini API provider and mock test provider) and structured tool-call definitions.
- **`agents/acp-adapter` (Go):** Adapter for Agent Client Protocol (ACP) integrations.
- **`apps/mobile` (Flutter / Dart):** Cross-platform Android & iOS client with QR pairing, platform secure storage (`flutter_secure_storage`), HKDF-SHA256 + AES-256-GCM E2E encryption (`FlutterE2ESession`), direct LAN/HTTPS E2E HTTP client (`DirectE2EHttpTransport`), and broker WebSocket client.
- **`packages/*`:** Shared `api-contracts` (`openapi.yaml`), `design-tokens`, and `domain-models`.
- **`docs/`:** Existing architecture, ADRs (`docs/adr/`), security specs (`docs/security/`), and stage reports (`docs/releases/`).

## 3. Key Architectural Invariants

1. **Host-side authority:** Source code, builds, Git repos, and AI tool execution remain on the host workstation.
2. **Workspace jail & command policy:** All filesystem and process operations are constrained to configured allowed roots and allowlisted binaries (`git`, `gradle`/`gradlew`, `adb`).
3. **Zero-trust Host Bridge HTTP boundary:** Every sensitive Host Bridge HTTP endpoint enforces session authentication, device/session binding, expiry, revocation, and least-privilege permissions on the Host Bridge itself (both direct LAN/HTTPS and broker-mediated paths).
4. **Channel-separated E2E encryption:** Direct HTTP (`/api/v1/e2e`) and Broker WebSocket use independent HKDF-SHA256 keys derived from the session master secret (`ChannelDirectHTTP = "http"` vs `ChannelBrokerWS = ""`), with AES-256-GCM, AAD binding, and monotonic sequence/replay protection.
5. **No raw bearer tokens over plaintext LAN:** Direct E2E HTTP (`/api/v1/e2e`) authenticates outer requests via HKDF-derived `HTTPAuthKey` HMAC-SHA256 signatures and rejects raw `Authorization: Bearer` headers.
6. **Production transport confidentiality:** In `production` mode, Host Bridge requires native TLS certificates or configured `DEVORBIT_TRUSTED_PROXIES` (binding plaintext upstream to loopback `127.0.0.1` only), and rejects plaintext host-admin or pairing-confirm requests.
