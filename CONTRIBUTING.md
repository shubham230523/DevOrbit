# DevOrbit Contributing Guidelines

Thank you for contributing to DevOrbit! Please follow these engineering guidelines to maintain quality, security, and test coverage across all components.

## Core Engineering Principles

1. **Clean Architecture & SOLID**: Keep business logic independent of frameworks and UI.
2. **Security-First Engineering**: Host bridge MUST enforce strict workspace allowlisting and path traversal prevention. Never allow arbitrary host filesystem access.
3. **Test-Driven & Evidence-Based**: Target 80%+ test coverage on core business logic. All pull requests must pass GitHub Actions CI checks (`.github/workflows/ci.yml`).
4. **No Web App Modules**: DevOrbit targets native Android & iOS mobile clients and Linux/Windows desktop host bridges. Do not introduce browser-specific UI modules.

## Repository Layout

- `apps/mobile/` - Flutter mobile workspace
- `services/control-plane/` - Go user authentication & machine registry
- `services/connection-broker/` - Go encrypted relay server
- `agents/host-bridge/` - Go desktop daemon (Linux/Windows)
- `agents/acp-adapter/` - Go Agent Client Protocol integration
- `agents/ai-router/` - Go AI provider orchestration
- `packages/api-contracts/` - OpenAPI specs & API definitions
- `infrastructure/` - Docker Compose, SQL schemas, deployment configs
- `docs/` - Architectural documentation, security specifications, ADRs

## Code Style & Formatting

- **Go**: Format code using `gofmt` or `goimports`. Run `golangci-lint` or standard `go test ./...`.
- **Flutter / Dart**: Format code using `dart format .`. Run `flutter analyze` and `flutter test`.

## Commit Standard

Commit messages should be clear and prefixed with component scopes, e.g.:
- `feat(host-bridge): implement path traversal sanitizer`
- `test(control-plane): add JWT verification unit tests`
- `docs(adr): record ADR-0001 architecture baseline`
