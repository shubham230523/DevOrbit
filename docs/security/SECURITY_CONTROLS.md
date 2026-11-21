# DevOrbit — Security Controls & Implementation Checklist

## 1. Cryptographic Key Management

- **Mobile Client**:
  - Android: `AndroidKeyStore` provider with `MasterKey` AES-256-GCM encryption for stored certificates and session tokens.
  - iOS: `Keychain Services` API with `kSecAccessControlBiometryAny` / passcode requirement.
- **Host Bridge**:
  - RSA 4096 or ECDSA P-256 host key generation during initial host bridge initialization (`devorbit host init`).
  - Stored in `%APPDATA%\devorbit\keys\` (Windows) or `~/.config/devorbit/keys/` (Linux) with file mode `0600` (read/write by user only).

## 2. Network Security Baseline

- **TLS 1.3 Minimum Baseline**: All network listeners enforce TLS 1.3 cipher suites.
- **Certificate Pinning**: Mobile clients pin the Host Bridge certificate fingerprint exchanged during initial QR pairing.
- **Connection Broker Isolation**: Broker acts solely as a TCP/WSS layer 4 stream relay. The broker has no private keys to decrypt E2E mobile-to-host payloads.

## 3. Host Bridge Security Enforcement

1. **Workspace Allowlister**:
   - Maintains `config.json` containing `allowed_workspaces`:
     ```json
     {
       "allowed_workspaces": [
         "C:\\Users\\shubham\\Documents\\PersonalProjects\\DevOrbit",
         "/home/developer/projects/AndroidApp"
       ]
     }
     ```
   - Rejects any operation attempting to operate outside these explicitly configured directories.

2. **File Size Limits**:
   - Default maximum editable file size: 10 MB. Binary files (images, compiled classes, native `.so`/`.dll`) are served as read-only preview/metadata.

3. **User Execution Approvals**:
   - Destructive operations (`git reset --hard`, file deletions, executing shell tasks) require active user confirmation from the mobile app interface.
