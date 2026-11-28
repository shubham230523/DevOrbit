# DevOrbit — Architectural & Security Decisions

Decisions recorded here must not be reversed without explicit justification, security review, and updated tests.

## 1. Platform & Connectivity Scope (Permanent MVP Scope)

- **Decision:** Target Android and iOS clients (`apps/mobile`) connecting to Linux and Windows development hosts (`agents/host-bridge`) over both local Wi-Fi/LAN and internet broker relay (`services/connection-broker`). Defer web application / browser IDE.
- **Reasoning:** Keeps compute, SDKs, builds, and source code on the developer's workstation while avoiding mobile resource exhaustion and browser-IDE scope creep.

## 2. Host Bridge Zero-Trust HTTP Auth Boundary (Master Plan 23)

- **Decision:** Every sensitive Host Bridge HTTP endpoint enforces session authentication, `(session_id, device_id)` binding, expiry, revocation, and `ActionPermission` authorization on the Host Bridge itself. Broker-mediated requests (`ServeBrokerRequest`) and direct HTTP requests share the exact same `SessionStore` and ownership checks.
- **Reasoning:** Prevents LAN or misconfigured-proxy callers from bypassing broker authentication by hitting Host Bridge directly.

## 3. Production Listener Confidentiality & Reverse-Proxy Trust (Master Plan 24)

- **Decision:** In `production` mode (`DEVORBIT_ENV=production`), `config.LoadConfig` requires either native TLS (`DEVORBIT_TLS_CERT_FILE` + `DEVORBIT_TLS_KEY_FILE`) or `DEVORBIT_TRUSTED_PROXIES` with loopback-only `BindAddr` (`127.0.0.1` by default; non-loopback rejected without native TLS). `authorizeHostAdmin` and `HandleConfirmPairing` require `r.TLS != nil` or a matching trusted proxy with `X-Forwarded-Proto: https`.
- **Reasoning:** Advertising an `https://` URL alone does not protect an exposed plain HTTP listener. Binding upstream to loopback behind a trusted proxy (or serving native TLS) closes direct plaintext bypasses and blocks spoofed `X-Forwarded-Proto` headers.

## 4. Pairing `v1.1` Anti-Replay Proof & Retry Window Invalidation (Master Plan 24)

- **Decision:** `ConfirmPairing` requires per-attempt `timestamp` and `nonce` bound into the `v1.1` HMAC-SHA256 proof (`1.1:challengeID:payload:hostID:deviceID:expiresAtStr:timestamp:nonce`), consumes nonces only after proof verification succeeds, and permanently invalidates the same-device retry cache (`InvalidateSessionRetry` + `pairingCache` deletion) as soon as the session is used (`MarkSessionUsed`) or revoked.
- **Reasoning:** Prevents captured confirmation requests from being replayed while preserving legitimate transient-failure recovery for the device that actually scanned the QR code.

## 5. `device_public_key` Wire Compatibility Without False Crypto Claims (Master Plan 24)

- **Decision:** Retain `device_public_key` in the `/api/v1/pairing/confirm` JSON payload for wire/OpenAPI compatibility, but remove it from Host Bridge `ConfirmPairing` parameters and explicitly document it as unused cryptographically; pairing response confidentiality relies on HTTPS/TLS in production.
- **Reasoning:** Previously `DevicePublicKey` was passed to `ConfirmPairing` without participating in any cryptographic operation, creating a misleading security claim. Inventing ad-hoc asymmetric wrapping was rejected in favor of standard TLS/HTTPS.

## 6. Direct E2E HTTP Outer Auth Without Raw Bearer Tokens (Master Plan 24)

- **Decision:** `POST /api/v1/e2e` (Go `HandleE2ERequest` and Flutter `DirectE2EHttpTransport`) authenticates outer requests using `X-DevOrbit-Session-ID`, `X-DevOrbit-Device-ID`, `X-DevOrbit-Timestamp`, `X-DevOrbit-Nonce`, and `X-DevOrbit-Signature` (`ComputeHTTPRequestMAC` with HKDF-derived `HTTPAuthKey`), and explicitly rejects raw `Authorization: Bearer` headers (`401`). Inner payloads use `ChannelDirectHTTP` (`"http"`) HKDF-separated AES-256-GCM keys.
- **Reasoning:** Eliminates raw session bearer token exposure on plaintext LAN HTTP while keeping Go and Flutter clients compatible and preserving channel separation from Broker WebSocket frames.

## 7. Official AI Integrations Primary

- **Decision:** Official agent integrations (e.g., ACP via `agents/acp-adapter`) remain the primary AI integration path, with direct provider APIs (`agents/ai-router`, e.g., Gemini API) as additional options.
- **Reasoning:** Preserves provider authentication semantics, tool compatibility, and entitlement boundaries.
