# DevOrbit — Security Threat Model (STRIDE Framework)

## 1. Overview
The DevOrbit security model operates under zero-trust assumptions between components. The mobile application and desktop host bridge are separated by network boundaries and distinct privilege domains.

## 2. STRIDE Analysis Matrix

| Threat Category | Potential Risk | Impact | Mitigations |
|---|---|---|---|
| **Spoofing** | Unauthorized mobile client attempts to connect to a user's Host Bridge. | High | QR Code mTLS pairing; cryptographic key exchange stored in Android Keystore / Apple Keychain; Host Bridge rejects non-paired certificates. |
| **Tampering** | Man-in-the-middle attacker alters code diffs or shell commands in transit. | Critical | End-to-end TLS 1.3 encryption on LAN and mTLS tunneled WSS on internet connections. Broker cannot inspect payload bytes. |
| **Repudiation** | User or agent performs dangerous operation without audit trail. | Medium | Host Bridge records immutable audit log of file writes, process executions, and Git operations with session IDs. |
| **Information Disclosure** | Leakage of API keys, source code, or workspace contents to Connection Broker or unauthorized parties. | Critical | Zero source code stored on Control Plane or Connection Broker. Local secrets stored in hardware keychains. Strict path sanitization. |
| **Denial of Service** | Malicious script consumes host CPU/Memory via unbounded process spawn or infinite stream. | High | Host Bridge enforces process timeouts, maximum file size read limits (e.g. 10MB), and websocket rate-limiting per session. |
| **Elevation of Privilege** | Path traversal flaw allows AI agent or remote client to read/write outside project root (e.g., `/etc/shadow` or `C:\Windows`). | Critical | Host Bridge PathSanitizer validates all canonical symlinked paths against an explicit workspace allowlist. Rejects paths outside allowlist. |

## 3. High-Risk Attack Vectors & Safeguards

### Path Traversal via Symlinks or `..`
- **Attack**: Request path `../../../../root/.ssh/id_rsa` or a relative symlink pointing outside the workspace.
- **Safeguard**: All input paths are cleaned using `filepath.Clean()`, resolved via `filepath.EvalSymlinks()`, and checked for prefix containment against canonical allowlist roots before any read/write/list operation.

### Remote Code Execution via Unsanitized Command Injection
- **Attack**: Remote request to run custom shell script with injected subcommands (`gradle build; rm -rf /`).
- **Safeguard**: Host Bridge NEVER passes command strings to arbitrary shell interpreters (`sh -c` or `cmd /c`). All process executions use explicit binary allowlists (`git`, `gradlew`, `adb`) with typed parameter arrays (`exec.Command(binary, args...)`).
