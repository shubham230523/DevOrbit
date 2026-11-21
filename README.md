# DevOrbit

**Your development environment, accessible from anywhere.**

DevOrbit is a mobile-first remote development workspace that lets developers access, manage, and work on their existing development projects from Android and iOS devices.

Instead of running a complete IDE, Android SDK, Gradle builds, or emulators on a mobile device, DevOrbit connects to a development computer running Linux or Windows. The development computer performs the actual engineering workloads while the mobile application provides a secure remote interface.

The goal is to make professional development workflows accessible from a phone or tablet without sacrificing the capabilities of a full development machine.

> **Project status:** Initial development and architecture phase.

---

## 1. Vision

Build a unified mobile development workspace that connects:

- Android and iOS mobile devices.
- Linux and Windows development computers.
- Existing local development projects and Git repositories.
- GitHub repositories and workflows.
- AI coding agents and model providers.
- Remote terminals, builds, tests, logs, and development tools.

DevOrbit aims to bring the convenience of a mobile IDE together with the computational power of a desktop development environment.

## 2. Core Philosophy

**The development computer does the work. The mobile device provides the interface.**

DevOrbit does not attempt to turn a phone into a full development computer.

Instead:

- Source code remains on the development host.
- Builds and tests execute on the development host.
- Android SDK and Gradle remain on the development host.
- AI agents operate against authorized host workspaces.
- Git operations execute against the actual repository.
- Mobile devices securely access and control these capabilities.

This architecture avoids unnecessary mobile resource consumption while preserving access to real development environments.

## 3. Supported Platforms

| Component | Initial support |
|---|---|
| Mobile client | Android |
| Mobile client | iOS |
| Development host | Linux |
| Development host | Windows |
| Connectivity | Local Wi-Fi / LAN |
| Connectivity | Internet / Remote access |
| Source control | Git and GitHub |
| AI integration | Official agent integrations and provider APIs |

**Out of scope for the initial release:**
- Web application or browser-based IDE.
- Running Android Studio directly on a phone.
- Running Android emulators on mobile devices.
- Replacing existing desktop IDEs.
- Unrestricted remote shell access.

## 4. Key Features

### 4.1 Remote Development Workspace

- Connect to registered Linux and Windows development computers.
- Discover existing development projects.
- Browse project directories and files.
- Read, edit, and save source files.
- Search within project workspaces.
- View file changes and handle concurrent-edit conflicts.
- Support phones, tablets, landscape layouts, and external keyboards.

### 4.2 Remote Development Operations

Execute development workloads on the connected host:

- Run terminal commands through controlled sessions.
- Execute Gradle builds.
- Run unit and integration tests.
- Inspect build output and test reports.
- Stream development logs.
- Access Android Logcat.
- Discover authorized Android devices connected to the host.
- Install and manage APK artifacts on authorized devices.

### 4.3 Git and GitHub Integration

- Connect GitHub accounts securely.
- Discover and access authorized repositories.
- Clone repositories to registered development hosts.
- Inspect branches, status, and diffs.
- Commit, pull, and push changes.
- Create and inspect pull requests.
- Detect and report merge conflicts.

Git remains the source-control authority. DevOrbit should never silently overwrite existing repository changes.

### 4.4 AI Coding Workspace

DevOrbit will provide a unified interface for supported AI coding agents and model providers.

Planned integrations include:

- Officially supported Antigravity agent integration, subject to interface and authentication compatibility.
- Agent integrations through supported protocols such as ACP.
- Gemini API.
- OpenAI API.
- Anthropic API.
- Additional supported model providers.

The AI integration layer will distinguish between official coding-agent access and direct model API access.

Model availability, authentication requirements, supported tools, usage limits, and account entitlements must reflect actual provider capabilities.

**Important:** Free access to a model through one product does not automatically provide free API access to that model.

### 4.5 Secure Remote Connectivity

DevOrbit is designed to support two connection modes:

**Local network**
- Direct communication between the mobile application and development host.
- Authenticated encrypted connections.
- Mutual TLS where supported.

**Internet access**
- Secure outbound connection from the development host to the DevOrbit connection infrastructure.
- Encrypted communication and authenticated sessions.
- Short-lived credentials.
- No requirement to expose an unrestricted public SSH or shell port.

Both modes must support secure pairing, device revocation, session expiration, and reconnection.

---

## 5. Architecture

DevOrbit follows a host-centric architecture.

```text
                  ANDROID / iOS
                Flutter Mobile App
                        |
                        |
              Authentication & API
                        |
                DEVORBIT CONTROL
                   PLANE / BROKER
                        |
             Secure Connection Layer
                        |
             +----------+----------+
             |                     |
        LINUX HOST            WINDOWS HOST
        Go Host Bridge        Go Host Bridge
             |                     |
       Project Files          Project Files
       Git Repositories       Git Repositories
       Gradle / Android SDK   Gradle / Android SDK
       Terminals              Terminals
       Connected Devices      Connected Devices
       AI Agent Runtime       AI Agent Runtime
```

### Main Components

| Component | Technology | Responsibility |
|---|---|---|
| Mobile application | Flutter / Dart | Android and iOS interface |
| Control plane | Go | Authentication, devices, sessions, permissions |
| Connection broker | Go | Secure remote connectivity and routing |
| Host bridge | Go | Host-side filesystem and development operations |
| Database | PostgreSQL | Server-side application state |
| Local persistence | SQLite | Mobile-side cache and local state |
| API contracts | OpenAPI | Versioned service interfaces |
| Real-time communication | WebSocket | Events, terminal output, and session updates |
| Agent integration | ACP and supported official interfaces | AI coding-agent communication |
| Source control | Git CLI and GitHub API | Repository operations |

The architecture must maintain strict separation between control-plane authorization and host-side execution permissions.

The host bridge independently validates every filesystem and process operation.

---

## 6. Security Principles

Security is a core architectural requirement, not a feature to add later.

DevOrbit will follow these principles:

- Secure device pairing and identity management.
- Encrypted communication.
- Authentication and authorization for every sensitive operation.
- Short-lived access credentials where appropriate.
- Android Keystore and Apple Keychain for mobile secrets.
- Host-side filesystem boundaries and path validation.
- Protection against path traversal and symlink abuse.
- Controlled process execution and command authorization.
- No unrestricted filesystem access.
- No unrestricted shell access.
- Session expiration and device revocation.
- Rate limiting and replay protection.
- Security audit events.
- Safe handling of Git credentials and AI provider credentials.

Credentials must never be hardcoded, committed to Git, or exposed in logs.

---

## 7. Technology Stack

### Mobile
- Flutter
- Dart
- Native Android and iOS integrations where necessary
- Android Keystore
- Apple Keychain

### Backend and Host
- Go
- PostgreSQL
- SQLite
- OpenAPI
- WebSocket
- Mutual TLS

### Development and Integration
- Git
- GitHub API
- Gradle
- Android SDK
- Android Debug Bridge
- ACP where supported
- Gemini API
- OpenAI API
- Anthropic API

### Engineering Infrastructure
- GitHub Actions
- Docker where appropriate
- Automated testing
- Static analysis
- Dependency and security scanning

---

## 8. Development Roadmap

DevOrbit will be developed through controlled engineering stages.

| Stage | Description |
|---|---|
| Stage 0 | Engineering foundation, architecture, repository, and CI |
| Stage 1 | Linux and Windows development host bridge |
| Stage 2 | Secure LAN and internet connectivity |
| Stage 3 | Flutter Android and iOS mobile application |
| Stage 4 | Git and GitHub integration |
| Stage 5 | AI coding agents and model integrations |
| Stage 6 | Remote Android development workflows |
| Stage 7 | Security, reliability, and performance hardening |
| Stage 8 | Beta packaging, installation, and release verification |

Each stage must have documented acceptance criteria and automated verification.

---

## 9. Engineering Standards

DevOrbit will follow professional software engineering practices:

- Clean Architecture.
- SOLID principles.
- Modular design and dependency inversion.
- Test-driven development for critical functionality.
- At least 80% meaningful unit-test coverage as a project target.
- Integration and contract testing.
- Security and failure-path testing.
- Cross-platform compatibility verification.
- Structured logging and actionable error reporting.
- Documentation for architecture and significant design decisions.
- Reviewable commits and dedicated feature branches.

No stage should be considered complete merely because the application compiles.

Reports must distinguish between mocked tests, simulated environments, cross-compilation, and actual platform verification.

---

## 10. Repository Structure

The initial monorepo is expected to evolve toward the following structure:

```text
devorbit/
├── apps/
│   └── mobile/
│       ├── android/
│       ├── ios/
│       └── lib/
│
├── services/
│   ├── control-plane/
│   └── connection-broker/
│
├── hosts/
│   └── bridge/
│       ├── linux/
│       └── windows/
│
├── packages/
│   ├── api-contracts/
│   ├── shared-protocol/
│   └── agent-adapters/
│
├── infrastructure/
│   ├── docker/
│   └── ci/
│
├── docs/
│   ├── architecture/
│   ├── adr/
│   ├── security/
│   └── reports/
│
├── scripts/
├── tests/
├── README.md
└── LICENSE
```

The final structure must be established after inspecting the actual implementation requirements during Stage 0. This layout is an initial organizational proposal, not a mandate to create empty modules prematurely.

---

## 11. Project Principles

1. **Host-first execution:** Heavy development workloads stay on the development computer.
2. **Mobile-first experience:** The mobile application is designed specifically for phone and tablet workflows.
3. **Cross-platform hosts:** Linux and Windows are first-class development environments.
4. **Security by design:** Remote access must never compromise host or repository security.
5. **Official integrations first:** Prefer supported APIs and protocols over undocumented interfaces.
6. **No artificial limitations:** Expose actual host capabilities without pretending mobile devices can replace desktop hardware.
7. **Correctness over shortcuts:** Reliability, test coverage, and data integrity take priority over implementation speed.
8. **Incremental verification:** Every stage must produce measurable and independently verifiable results.

---

## 12. Current Status

**Initial development phase**

The first milestone is to establish the engineering foundation and validate the architecture before implementing the complete product.

The project is not considered production-ready until the Android and iOS clients, Linux and Windows host bridges, secure connectivity, AI integrations, and remote development workflows have been verified against their respective acceptance criteria.

---

## 13. Long-Term Vision

DevOrbit aims to make a developer's existing computer accessible as a complete remote engineering workspace.

A developer should be able to:

- Open an existing Android Studio project from a phone.
- Inspect and edit its source code.
- Ask an AI coding agent to understand or modify the project.
- Review AI-generated changes.
- Execute builds and tests remotely.
- Inspect logs and test results.
- Commit and push changes to GitHub.
- Continue working from another device without moving the development environment.

**One mobile workspace. Multiple development machines. One unified engineering experience.**

---

*DevOrbit — Develop anywhere. Build on your machine.*