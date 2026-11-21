# DevOrbit — QR Pairing & Device Identity Security

## 1. QR Code Pairing Challenge Flow

```
Mobile App                        Host Bridge / Control Plane
    |                                        |
    |  1. Scan QR Code                       |
    |  (Challenge ID + Host ID + Payload)    |
    |--------------------------------------->|
    |                                        |
    |  2. Submit Proof Signature             |
    |  HMAC-SHA256(Secret, Payload + DevID)  |
    |--------------------------------------->|
    |                                        |
    |  3. Verify Proof & Invalidate QR       |
    |  Issue Session Token                   |
    |<---------------------------------------|
```

## 2. Security Safeguards

- **Single-Use Enforcement**: Pairing challenges are deleted or marked consumed upon first confirmation. Replaying a QR payload yields `400 Challenge Consumed`.
- **Short Lifetime**: Pairing challenges expire after 5 minutes (`300s`).
- **Cryptographic Key Storage**:
  - Android: `AndroidKeyStore` AES-256-GCM encrypted key storage.
  - iOS: `Keychain Services` with biometrics requirement.
  - Host Bridge: Platform protected user directory config (`0600` permissions).
- **Revocation Enforcement**: Revoked devices are barred from issuing new pairing confirmations or authorizing broker sessions.
