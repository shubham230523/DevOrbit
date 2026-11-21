# DevOrbit — Secure Connectivity Architecture Specification (v1.2.1)

## 1. Dual-Mode Connectivity Architecture

DevOrbit supports two secure communication modes between mobile clients (Android & iOS) and workstation development hosts (Windows & Linux):

```
                   MOBILE CLIENT (Flutter)
                             |
                   +---------+---------+
                   |                   |
            Mode A: Direct LAN   Mode B: Internet Relay
                   |                   |
               Direct mTLS        TLS WSS Relay
                   |                   |
                   |           Connection Broker
                   |      (Opaque AES-GCM Frame Router)
                   |                   |
                   +---------+---------+
                             |
                   HOST BRIDGE (Go Daemon)
                             |
                 Workspace Security Jail
```

## 2. Cryptographic Baseline & Key Lifecycle

- **HKDF SHA-256 Key Derivation**: Directional subkeys (`client_to_host_key` vs `host_to_client_key`) derived from session secrets using HKDF with unique session ID salt and context info.
- **Authenticated Associated Data (AAD)**: Every encrypted payload binds `session_id`, `device_id`, `seq_num`, and `timestamp` as AAD to prevent cross-session / cross-device / reordering payload substitution.
- **Replay Protection**: Monotonically verified sequence numbers and per-session replay cache reject duplicate or reordered frame ciphertexts.
- **Single-Use QR Pairing**: Short-lived pairing challenges (5 minutes) invalidated immediately upon first consumption with host-side rate limiting (max 5 failed attempts per client).
