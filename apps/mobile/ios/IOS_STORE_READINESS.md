# DevOrbit iOS — Store Readiness & Configuration Notes

## 1. What the repository now enforces

| Item | Value / Implementation |
|---|---|
| Camera rationale | `NSCameraUsageDescription` explains QR-only usage and that nothing is stored |
| Local network | `NSLocalNetworkUsageDescription` added — required from iOS 14 for LAN access |
| App Transport Security | `NSAllowsLocalNetworking = true` only; **no** `NSAllowsArbitraryLoads`; internet/broker traffic stays on default ATS (TLS 1.2+) |
| Bonjour | Not declared — the app has no Bonjour/mDNS discovery. Add `NSBonjourServices` only if discovery is implemented later |
| Background execution | No `UIBackgroundModes` declared. The app does **not** promise background task execution: an AI task continues only while the app is in the foreground; on resume the client re-polls task state |
| Credentials | Keychain via `flutter_secure_storage` (`KeychainAccessibility.first_unlock`) |
| Bundle identifier | `$(PRODUCT_BUNDLE_IDENTIFIER)` — set this to your own reverse-DNS id in Xcode (no identifier is invented in this repo) |

## 2. Behaviour you must be aware of (product truth)

- **Foreground-only task streaming.** If the app is backgrounded during a long AI task, the WebSocket
  may be suspended by iOS. On return the app reconnects and re-polls; it does not claim to have kept
  a live connection while suspended.
- **LAN over HTTP is developer-only.** A CA-trusted HTTPS Host Bridge is required for any
  non-developer deployment. `NSAllowsLocalNetworking` exists to keep local pairing workable, not to
  permit plaintext internet traffic.

## 3. Prerequisites YOU must provide (not creatable in this repository)

1. **macOS with Xcode 15+** and the iOS platform installed.
2. **Apple Developer Program** membership and an App Store Connect app record.
3. **Bundle identifier** (e.g. `com.<yourorg>.devorbit`) set in Xcode → Runner → Signing & Capabilities.
4. **Signing**: development and distribution certificates plus provisioning profiles (Xcode-managed
   recommended), and a team id.
5. **App Privacy answers** (App Store Connect): data linked to the user includes
   - *User content* — prompts and workspace file content sent to the configured AI provider,
   - *Identifiers* — host/device/session identifiers stored on the paired host.
   Both are required for App Review accuracy.
6. **Review demo plan.** Reviewers cannot use the product without a paired host. Provide either a
   demo video of pairing + task execution, or a reachable demo host with temporary credentials in
   the review notes. Without this, App Review rejection is likely.

## 4. Verification status (honest)

| Check | Status |
|---|---|
| `Info.plist` configuration implemented | **IMPLEMENTED** (repository) |
| Xcode compile / archive | **BLOCKED** — no macOS/Xcode toolchain on this Windows host |
| Simulator or physical-device runtime test | **BLOCKED** — no iOS device, no simulator |
| TestFlight upload | **BLOCKED** — requires Apple account + macOS |
| App Review | **BLOCKED** — requires shipped build |

Re-run this checklist on macOS:

```bash
cd apps/mobile
flutter build ios --release --no-codesign   # compile validation only
flutter build ipa --export-method app-store # requires signing configuration
plutil -lint ios/Runner/Info.plist          # plist syntax validation
```
