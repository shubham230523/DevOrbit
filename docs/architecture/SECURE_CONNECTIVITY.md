# DevOrbit — Secure Connectivity Architecture Specification

## 1. Dual-Mode Connectivity Model

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
                   |           (Opaque Frame Router)
                   |                   |
                   +---------+---------+
                             |
                   HOST BRIDGE (Go Daemon)
                             |
                 Workspace Security Jail
```

### Mode A — Direct LAN Mode (mTLS)
- **Transport**: Direct TCP/HTTPS with Mutual TLS (mTLS) over local Wi-Fi.
- **Identity Verification**: Mobile client verifies Host Certificate fingerprint pinned during initial QR pairing. Host Bridge verifies client device certificate.
- **Latency**: Very low (< 5ms local latency).

### Mode B — Internet Relay Mode (Connection Broker)
- **Transport**: Outbound WebSocket connections (`wss://`) established from Host Bridge and Mobile Client to Connection Broker.
- **Host Exposure**: ZERO open inbound public ports required on the workstation host.
- **Opaque Relay**: Connection Broker routes binary frames using session IDs. Payload is End-to-End Encrypted (AES-256-GCM / Noise protocol); broker cannot inspect or decrypt workspace content or shell payloads.

## 2. Pairing & Identity Lifecycle

- **Device States**: `UNREGISTERED` -> `PENDING_PAIRING` -> `ACTIVE` -> `REVOKED` / `EXPIRED`.
- **Single-Use Challenge**: QR code pairing challenge expires after 5 minutes and is invalidated immediately upon consumption.
- **Proof of Possession**: Mobile client submits HMAC-SHA256 signature proving key ownership.
- **Immediate Revocation**: Revoking a device on Control Plane or Host Bridge immediately terminates all active LAN and Broker sessions.
