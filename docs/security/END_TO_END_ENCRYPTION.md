# DevOrbit — End-to-End Encryption (E2E) & Key Lifecycle Specification

## 1. Cryptographic Key Derivation (HKDF SHA-256)

Session master secrets derived during pairing are expanded into direction-specific 256-bit subkeys using HKDF (HMAC-SHA256):

```
                       Master Session Secret
                                 |
                               HKDF
                                 |
           +---------------------+---------------------+
           |                                           |
    Client-to-Host Key                          Host-to-Client Key
 (AES-256-GCM Encryption)                   (AES-256-GCM Encryption)
```

- **Salt**: `devorbit-e2e-salt-<session_id>`
- **Info (Client -> Host)**: `devorbit-client-to-host-<session_id>-<device_id>`
- **Info (Host -> Client)**: `devorbit-host-to-client-<session_id>-<device_id>`

## 2. Authenticated Associated Data (AAD) Binding

To prevent packet substitution or metadata tampering by intermediary relays (Connection Broker), every AES-256-GCM encryption binds an AAD context byte array:

$$\text{AAD} = \text{SessionID} \parallel ":" \parallel \text{DeviceID} \parallel ":" \parallel \text{BigEndian}(\text{SeqNum}) \parallel \text{BigEndian}(\text{Timestamp})$$

If the Connection Broker or an attacker alters the `session_id`, `device_id`, or sequence number in transit, the AES-256-GCM tag verification fails at the receiving host, rejecting the tampered frame.

## 3. Key Rotation & Zeroization

When a session expires or a device is revoked, keys are destroyed in memory (`b[i] = 0`) to prevent post-session memory inspection vulnerabilities.
