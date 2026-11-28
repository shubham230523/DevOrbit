# DevOrbit Android Release Signing Setup

This document explains how **you** create and configure the production upload keystore. No signing
material is stored in this repository, and a release build is **refused** until you provide it.

> [!IMPORTANT]
> A release build never falls back to the debug keystore. If credentials are missing,
> `assembleRelease` / `bundleRelease` fail with:
> `DevOrbit release build refused: no release signing credentials configured.`

## 1. Create the upload keystore (once)

Use a JDK 17 `keytool` (bundled with Android Studio or any JDK). Store the file **outside** the
repository, for example `%USERPROFILE%\.devorbit-keys\upload-keystore.jks`.

```bash
keytool -genkeypair -v \
  -keystore /path/to/upload-keystore.jks \
  -storetype PKCS12 \
  -keyalg RSA -keysize 4096 -validity 10000 \
  -alias upload \
  -storepass "<STRONG_STORE_PASSWORD>" \
  -keypass "<STRONG_KEY_PASSWORD>" \
  -dname "CN=DevOrbit, OU=Mobile, O=<YOUR_ORG>, L=<CITY>, ST=<STATE>, C=<COUNTRY>"
```

Guidance:

- Use **PKCS12** (industry standard); JKS is legacy.
- 4096-bit RSA and a validity of 10000 days is sufficient for Play upload keys.
- Keep at least two offline backups of the keystore and passwords. **Losing it prevents app updates**
  unless Play App Signing key reset is used.
- Enable **Play App Signing** when creating the app record so Google holds the app signing key and
  your keystore only signs uploads.

## 2. Provide credentials (choose one)

### Option A — environment variables (recommended, and required for CI)

```powershell
$env:DEVORBIT_ANDROID_KEYSTORE_PATH="C:\Users\<you>\.devorbit-keys\upload-keystore.jks"
$env:DEVORBIT_ANDROID_KEYSTORE_PASSWORD="<STRONG_STORE_PASSWORD>"
$env:DEVORBIT_ANDROID_KEY_ALIAS="upload"
$env:DEVORBIT_ANDROID_KEY_PASSWORD="<STRONG_KEY_PASSWORD>"
```

### Option B — untracked `key.properties`

Copy [key.properties.example](key.properties.example) to `android/key.properties` and fill in the
values. This path is git-ignored (`apps/mobile/android/.gitignore`).

## 3. Build and verify

```bash
cd apps/mobile
flutter build appbundle --release   # Play upload artifact (build/app/outputs/bundle/release/)
flutter build apk --release         # Optional sideload artifact
```

Verify the signature of a produced artifact:

```bash
# APK
apksigner verify --print-certs build/app/outputs/flutter-apk/app-release.apk
# AAB
jarsigner -verify -verbose -certs build/app/outputs/bundle/release/app-release.aab | tail -5
```

The reported certificate must be **yours** (`CN=DevOrbit...`), not `CN=Android Debug`.

## 4. What is enforced by the build

| Control | Implementation |
|---|---|
| No committed secrets | `key.properties`, `*.jks`, `*.keystore` ignored at repo root and under `android/` |
| No debug fallback | `release.signingConfig` is `null` without credentials; task-graph guard throws |
| Modern signature schemes | v2/v3/v4 enabled, v1 disabled |
| Fail-closed release | `gradle.taskGraph.whenReady` refuses `assembleRelease`, `bundleRelease`, `packageRelease*`, `signReleaseBundle` |

## 5. CI integration

The workflow (`.github/workflows/ci.yml`) builds a signed release **only** when these repository
secrets are configured, and never for pull requests from forks:

- `ANDROID_KEYSTORE_BASE64` — `base64 -w0 upload-keystore.jks`
- `ANDROID_KEYSTORE_PASSWORD`
- `ANDROID_KEY_ALIAS`
- `ANDROID_KEY_PASSWORD`

Without them the release job is skipped; the unsigned/debug verification jobs still run.

## 6. Rotation and incident response

1. Generate a new upload keystore.
2. Upload the new certificate to Play Console (App integrity → Upload key certificate → Request
   upload key reset) if the old key is compromised.
3. Revoke CI secrets, rotate passwords, and invalidate old artifacts.
4. Record the rotation in the engineering execution log (never the key material).
