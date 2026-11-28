# DevOrbit — Known Issues, Risks & Limitations

## 1. Closed / Remediated in Master Plan 24 Closure

- ~~Session-store empty-permission restoration & plaintext `session_secret` persistence (`session_store.go`, `broker_client.go`)~~ — **RESOLVED** via v2 schema without `omitempty`, OS-backed `SecretStore` (Windows DPAPI / Linux Secret Service) with memory-only fail-closed fallback, atomic temp+fsync+rename writes, durable revocation tombstones, legacy plaintext rejection, and error propagation.
- ~~Mobile pairing HTTPS-to-HTTP fallback downgrade (`pairing_client.dart`)~~ — **RESOLVED** by restricting localhost/`10.0.2.2` fallback strictly to `scheme == 'http'` (HTTPS never falls back or downgrades).
- ~~Multi-valued `X-Forwarded-Proto` ambiguity (`auth_middleware.go`)~~ — **RESOLVED** by requiring `len(r.Header.Values("X-Forwarded-Proto")) == 1`.
- ~~`cmd/host-bridge` Linux cross-compilation error (`syscall.NewLazyDLL`)~~ — **RESOLVED** via `console_windows.go` and `console_other.go`.

## 2. Active Blockers & Environment Limitations

- **Live TLS reverse-proxy & physical mobile validation (`BLOCKED`):** Cannot run on this workstation (`Windows NT 10.0.26200.0`) because `caddy`/`nginx` are not installed, no CA-trusted server certificate is provisioned, and `adb devices -l` shows zero attached physical devices or emulators.
- **Go `-race` detector on Windows host (`BLOCKED`):** Installed MinGW `cc1.exe` is 32-bit only (`sorry, unimplemented: 64-bit mode not compiled in`), preventing `go test -race` from compiling on this machine. Run `go test -race ./...` in Linux CI or a 64-bit MinGW/MSYS2 environment.
- **Headless Linux Secret Service UX behavior:** On headless Linux without an active D-Bus Secret Service (`secret-tool` + `DBUS_SESSION_BUS_ADDRESS`), Host Bridge intentionally refuses plaintext session-secret persistence (`ErrSecureStoreUnavailable`), persists revocation tombstones only, and requires mobile devices to re-pair after a Host Bridge restart.

## 3. Protocol & Scope Notes (Deferred / Unchanged)

- **Development-mode LAN HTTP pairing:** When `DEVORBIT_ENV != production` and native TLS is absent, local LAN HTTP pairing remains enabled for developer convenience. Production mode blocks plaintext pairing/host-admin endpoints (`403`).
- **`device_public_key` unused for response encryption:** Retained in `/api/v1/pairing/confirm` and `services/control-plane` for wire compatibility only; production confidentiality is enforced by HTTPS/TLS.
- **Control-plane pairing package:** `services/control-plane/internal/pairing/pairing.go` still uses its own `ConfirmPairing` signature separately from `agents/host-bridge` (`v1.1` timestamp/nonce proof). Out of MP24 scope unless control-plane-mediated pairing is activated.
