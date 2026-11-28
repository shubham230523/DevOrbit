# DevOrbit — Known Issues, Risks & Limitations

## 1. Closed / Remediated

### Master Plan 24 closure (`2425cc838c64e7e1e28c67e33c1636d45e5889d7`)

- ~~Session-store empty-permission restoration & plaintext `session_secret` persistence~~ — **RESOLVED** via v2 schema without `omitempty`, OS-backed `SecretStore` (Windows DPAPI / Linux Secret Service) with memory-only fail-closed fallback, atomic temp+fsync+rename writes, durable revocation tombstones, legacy plaintext rejection, and error propagation.
- ~~Mobile pairing HTTPS-to-HTTP fallback downgrade (`pairing_client.dart`)~~ — **RESOLVED** (fallback strictly limited to `scheme == 'http'`).
- ~~Multi-valued `X-Forwarded-Proto` ambiguity (`auth_middleware.go`)~~ — **RESOLVED** (`len(r.Header.Values(...)) == 1` required).
- ~~`cmd/host-bridge` Linux cross-compilation error (`syscall.NewLazyDLL`)~~ — **RESOLVED** via `console_windows.go` / `console_other.go`.

### Master Plan 24.1 (`76ca2286523b1978acec1e64723d74ec64cddf2d`)

- ~~`SessionStore.RevokeSession` returned `(false, nil)` for sessions absent from memory, letting `HandleRevokeSession` answer `200 OK` with no tombstone~~ — **RESOLVED**: durable, idempotent tombstone always written; persistence/secret-deletion failures returned to the caller.
- ~~`saveLocked` rebuilt persisted state from memory only, so one revocation in a process that had not loaded sessions erased every other durable session~~ — **RESOLVED**: persisted-only entries are merged and preserved (tombstoned/expired/device-revoked/in-memory entries excluded).
- ~~Windows DPAPI `Delete()` was a no-op because ciphertext lived inline in `secret_ref`, leaving recoverable references after an atomic JSON write failure~~ — **RESOLVED**: external per-session DPAPI files with opaque refs and a real `Delete()`; missing/deleted/corrupt secrets fail closed.
- ~~Pairing rollback discarded `RemoveSession` and broker-revocation errors (`_ = store.RemoveSession(...)`)~~ — **RESOLVED**: all rollback failures are reported alongside the original error, the in-memory `BrokerClient` session is revoked, and no success response is returned after partial provisioning.
- ~~`BrokerClient.RevokeSession/RevokeDevice` and `SessionRevoker` swallowed persistence failures; `RevokeDevice` remote errors were ignored~~ — **RESOLVED**: errors propagate; HTTP revocation returns `500` (durability) or `502` (remote) instead of `200 OK`.
- ~~Broker session-only revocation closed tunnels but left the session's client authorization token valid, allowing the revoked session to re-open a tunnel~~ — **RESOLVED**: `Broker.RevokeSession` deauthorizes the session's token and expiry before closing tunnels, leaving sibling sessions on the same device authorized.

## 2. Active Blockers & Environment Limitations

- **Live TLS reverse-proxy & physical mobile validation (`BLOCKED`):** cannot run on this workstation (`Windows NT 10.0.26200.0`) — `caddy`/`nginx` absent, no CA-trusted server certificate, `adb devices -l` empty.
- **Go `-race` detector on Windows host (`BLOCKED`):** only 32-bit MinGW (`C:\MinGW\bin\gcc.exe`) is installed, so `go test -race` fails at `# runtime/cgo` with `cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`. Run race-detector suites in Linux CI or with a 64-bit CGO toolchain.
- **Windows DPAPI validation is host-bound:** the `windows-dpapi-file-v2` store is exercised by native Windows tests only; cross-builds do not substitute for executing them.

## 3. Accepted Residual Risks (documented, not fixed)

- **No physical erasure of DPAPI secret files:** deleting a per-session `.dpapi` file removes OS-level access to the blob but does not guarantee physical erasure of historical filesystem blocks. Not claimed.
- **Orphaned sealed-secret files:** if `SecretStore.Delete` fails while the tombstone write succeeds, an unreferenced sealed blob may remain under `~/.devorbit/secrets`. It is unreachable from persisted state and unsealable without the same Windows user identity, but it consumes disk until cleaned up manually.
- **Headless Linux Secret Service UX:** without an active D-Bus Secret Service, the Host Bridge intentionally refuses plaintext session-secret persistence (`ErrSecureStoreUnavailable`), persists revocation tombstones only, and devices must re-pair after a restart.
- **`services/connection-broker/pkg/broker` is a legacy duplicate** of `internal/broker` and does not contain the `RevokeSession` deauthorization fix. It is not referenced by `cmd/connection-broker`; consolidating or deleting it is deferred to a separate change.

## 4. Protocol & Scope Notes (Deferred / Unchanged)

- **Development-mode LAN HTTP pairing:** when `DEVORBIT_ENV != production` and native TLS is absent, local LAN HTTP pairing stays enabled for developer convenience; production mode blocks plaintext pairing/host-admin endpoints (`403`).
- **`device_public_key` unused for response encryption:** retained for wire compatibility; confidentiality is enforced by HTTPS/TLS.
- **Control-plane pairing package:** `services/control-plane/internal/pairing/pairing.go` keeps its own `ConfirmPairing` signature; out of scope unless control-plane-mediated pairing is activated.
