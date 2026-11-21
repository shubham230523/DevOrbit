# DevOrbit Connection Broker Microservice

The DevOrbit Connection Broker is a high-concurrency Go relay service that facilitates secure remote connectivity between mobile clients (Android & iOS) and workstation development hosts (Windows & Linux) when operating off-LAN over the internet.

## Security Guarantees

1. **Zero Open Public Host Ports Required**: Workstation host bridges establish outbound WebSocket (`wss://`) connections to the broker.
2. **Opaque Frame Relaying**: The broker routes binary frame envelopes (`v1.2`) using session IDs without inspecting or decrypting application payloads.
3. **End-to-End Encryption**: All workspace contents, source code, diffs, and shell commands are encrypted using AES-256-GCM between mobile app and host bridge. The broker does NOT possess session encryption keys.
4. **Immediate Revocation**: Revoking a device on Control Plane or Broker immediately terminates all associated active WebSocket tunnels.

## Running the Connection Broker

```bash
# Build binary
go build -o bin/connection-broker ./cmd/connection-broker

# Run Connection Broker (default port: 8081)
./bin/connection-broker
```
