# DevOrbit — End-to-End Encryption (E2E) Specification

## 1. Cryptographic Baseline

DevOrbit implements End-to-End Encryption (E2E) for all workspace data, source code files, diffs, Logcat output, and shell commands transmitted over untrusted networks and relays.

- **Algorithm**: AES-256-GCM (Galois/Counter Mode) with 96-bit random nonces.
- **Key Derivation**: SHA-256 HKDF from mutually verified session secrets exchanged during paired authentication.
- **Nonce Generation**: `crypto/rand` cryptographically secure pseudorandom numbers per frame.

## 2. Connection Broker Inaccessibility

The Connection Broker acts solely as a Layer 4 framing relay.
- The broker receives `encrypted_payload` hex string.
- The broker DOES NOT possess the session's AES-256-GCM symmetric key.
- Attempting to inspect or modify `encrypted_payload` at the broker results in AES-GCM authentication tag mismatch upon decryption at the Host Bridge, rejecting the tampered payload.
