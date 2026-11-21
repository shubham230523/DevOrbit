# DevOrbit — Stage 2 Threat Model & Verification

## 1. STRIDE Threat Analysis for Connectivity

| Threat Category | Potential Risk | Mitigation in Stage 2 | Test Verification |
|---|---|---|---|
| **Spoofing** | Rogue client impersonating paired device | Proof-of-possession HMAC signature requirement during QR pairing and session authorization. | `TestPairing_LifecycleAndSecurity` |
| **Tampering** | Connection broker modifying source code frames in transit | AES-256-GCM authenticated encryption tag verification at Host Bridge. | `TestE2ECipher_EncryptDecryptAndTamperedPayload` |
| **Repudiation** | Client denies executing destructive command | Host Bridge records immutable audit log with device ID and session ID. | `TestTransport_HTTPAPIEndpointsAndPairing` |
| **Information Disclosure** | Broker inspecting source code or SSH keys | End-to-End Payload Encryption; Broker only sees opaque ciphertext. | `TestE2ECipher_EncryptDecryptAndTamperedPayload` |
| **Denial of Service** | Flooding broker with fake connection frames | Device authorization check at broker before registering client sessions. | `TestBroker_AuthorizationAndDeviceRevocation` |
| **Elevation of Privilege** | Revoked device continuing active session | Revoking device on broker immediately terminates all associated active WebSocket sessions. | `TestBroker_AuthorizationAndDeviceRevocation` |
