# DevOrbit — Security Acceptance & Vulnerability Mitigation Report

## 1. Security Controls Verification

1. **Path Traversal & Symlink Jail**: All filesystem paths are cleaned and evaluated via `filepath.EvalSymlinks()`. Traversal paths outside allowed workspace roots are rejected with `HTTP 403 Forbidden`.
2. **Command Injection Prevention**: Executables are checked against binary allowlist (`git`, `gradlew`, `./gradlew`, `gradle.bat`, `adb`). Shell syntax injection characters (`;&|`$<>`) are rejected.
3. **HKDF SHA-256 Key Derivation**: Directional subkeys (`client_to_host_key` vs `host_to_client_key`) derived via HKDF SHA-256.
4. **AES-256-GCM AAD Frame Binding**: Binds session ID, device ID, sequence number, and timestamp to payload ciphertext.
5. **Replay Protection**: Sequence numbers & replay cache reject duplicate or reordered frame ciphertexts.
6. **Rate Limiting & Lockout**: Failed QR pairing attempts are rate-limited (max 5 attempts before 10-minute lockout).
7. **Request-ID Idempotency**: Operation deduplication prevents duplicate process executions upon network reconnection.
8. **Human Approval Gate**: All AI agent tool proposals require explicit human confirmation via `AiToolApprovalDialog`.
9. **Instant Session Teardown**: Device revocation on Control Plane or Broker immediately closes active WebSocket channels.
