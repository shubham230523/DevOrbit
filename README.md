# DevOrbit

DevOrbit is a secure, cross-platform remote development mobile workspace for Android and iOS that connects developers to their existing desktop Android Studio development host environments (Linux & Windows).

## System Architecture Overview

- **`apps/mobile`**: Flutter + Dart client application targeting Android and iOS.
- **`services/control-plane`**: Go microservice for authentication, device pairing management, session authorization, and workspace metadata.
- **`services/connection-broker`**: Go microservice facilitating secure outbound WebSocket and mTLS connections between mobile clients and host bridges.
- **`agents/host-bridge`**: Portable Go daemon executing on developer Linux/Windows host workstations to securely manage project discovery, file operations, Git actions, Gradle builds, and Android device debugging.
- **`agents/acp-adapter`**: Adapter implementing Agent Client Protocol (ACP) for official agent integrations.
- **`agents/ai-router`**: Multi-provider AI orchestration layer managing Antigravity, Gemini, OpenAI, and Anthropic API connections.
- **`packages/api-contracts`**: OpenAPI specifications and protocol definitions governing inter-component RPC/REST communications.

## Development Requirements

- **Go**: 1.24+
- **Flutter**: 3.47+ (Dart 3.13+)
- **Git**: 2.28+
- **Docker & Docker Compose**: 29.0+
- **Android SDK & JDK 17**: Required on Host machines for building Android projects.

## Quick Start

### Go Services
```bash
# Test Control Plane
cd services/control-plane && go test ./...

# Test Connection Broker
cd services/connection-broker && go test ./...

# Test Host Bridge
cd agents/host-bridge && go test ./...
```

### Mobile Application
```bash
cd apps/mobile
flutter pub get
flutter test
```

## Documentation

Full architectural specifications, security threat models, ADRs, and verification reports are maintained in `docs/`:
- [System Architecture](docs/architecture/SYSTEM_ARCHITECTURE.md)
- [Component Design](docs/architecture/COMPONENT_DESIGN.md)
- [Data Flow Specification](docs/architecture/DATA_FLOW.md)
- [Threat Model](docs/security/THREAT_MODEL.md)
- [Security Controls](docs/security/SECURITY_CONTROLS.md)
- [Architecture Decision Records](docs/adr/)
- [Testing Strategy](docs/testing/TEST_STRATEGY.md)
