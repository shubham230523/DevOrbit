# DevOrbit Connection Broker Microservice

The DevOrbit Connection Broker is a high-concurrency Go relay service that facilitates secure remote connectivity between mobile clients (Android & iOS) and workstation development hosts (Windows & Linux) when operating off-LAN over the internet.

## Security Guarantees

1. **Zero Open Public Host Ports Required**: Workstation host bridges establish outbound WebSocket (`wss://`) connections to the broker.
2. **Opaque Frame Relaying**: The broker routes binary frame envelopes (`v1.2`) using session IDs without inspecting or decrypting application payloads.
3. **End-to-End Encryption**: All workspace contents, source code, diffs, and shell commands are encrypted using AES-256-GCM between mobile app and host bridge. The broker does NOT possess session encryption keys.
4. **Immediate Revocation**: A service-to-service revoke call (`/api/v1/internal/devices/revoke`) terminates the affected session or device tunnels and deauthorizes the session's client token.

## Package layout

The single canonical implementation lives in `pkg/broker` (import path
`devorbit.net/services/connection-broker/pkg/broker`). It is the only broker imported by
`cmd/connection-broker` and by the Host Bridge integration tests. The previous duplicated copy under
`internal/broker` was removed in MP26 (Task B) so that exactly one implementation of broker
authentication, revocation and frame handling exists.

## Running the Connection Broker

```bash
# Build binary
go build -o bin/connection-broker ./cmd/connection-broker

# Run Connection Broker (default port: 8081)
./bin/connection-broker
```
