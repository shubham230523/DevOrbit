# DevOrbit — Automated Testing Strategy & Acceptance Criteria

## 1. Testing Framework Overview

DevOrbit enforces automated testing across all layers to ensure high reliability, security, and performance.

| Layer | Component | Test Types | Framework / Tools | Target Coverage |
|---|---|---|---|---|
| **Mobile App** | `apps/mobile` | Unit Tests, Widget Tests, BLoC State Tests | Dart `test`, `flutter_test` | >= 80% |
| **Control Plane** | `services/control-plane` | Unit Tests, Service Mock Tests | Go `testing`, `testify` | >= 80% |
| **Connection Broker** | `services/connection-broker` | Unit Tests, Tunnel Mock Tests | Go `testing` | >= 80% |
| **Host Bridge** | `agents/host-bridge` | Unit Tests, Path Sanitizer Edge-Case Tests, Git/Gradle Executor Tests | Go `testing`, `testify` | >= 80% |
| **ACP Adapter & AI Router**| `agents/acp-adapter`, `agents/ai-router` | Provider Integration Tests, Parser Unit Tests | Go `testing` | >= 80% |

## 2. Security Edge-Case Test Requirements

The `agents/host-bridge` module must include dedicated security test suites verifying:
1. **Path Traversal Attacks**:
   - `../` traversal attempts (e.g. `workspace/../../secret.txt`).
   - Absolute path attempts outside allowlist (e.g. `/etc/passwd` or `C:\Windows\System32`).
   - Symlink traversal pointing to external host files.
2. **File Size Enforcement**:
   - Rejection of files exceeding maximum read limits.
3. **Command Execution Boundaries**:
   - Rejection of non-allowlisted binaries.

## 3. Continuous Integration Pipeline

GitHub Actions pipeline (`.github/workflows/ci.yml`) executes:
1. Go formatting (`gofmt`), static analysis (`go vet`), and unit tests across all Go modules.
2. Flutter pub get, linting (`flutter analyze`), and unit/widget tests.
3. OpenAPI contract validity check.
