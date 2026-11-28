# Live TLS + Physical Device Validation Procedure (MP24 Criterion I / MP25 M1)

**Status: NOT YET EXECUTED — BLOCKED on external dependencies.**
Repository-side prerequisites are implemented (`infrastructure/deployment/Caddyfile`,
`env.example`, broker/host TLS enforcement in `internal/config/config.go`). The steps below produce
the evidence that converts `IMPLEMENTED` into `VERIFIED`.

## Prerequisites (you must provide)

| # | Dependency | How to satisfy |
|---|---|---|
| 1 | DNS name for the broker, e.g. `broker.example.com` | A/AAAA record → public IP of the proxy host |
| 2 | DNS name for the host bridge, e.g. `host.example.com` | A/AAAA record → same or second host |
| 3 | Caddy v2 (or Nginx equivalent) | `caddy version` on the proxy host |
| 4 | Ports 80 + 443 reachable | Security group / firewall |
| 5 | Broker admin secret ≥ 32 chars | `openssl rand -base64 48` |
| 6 | Physical Android device on Wi-Fi | `adb devices -l` shows it |
| 7 | Physical iPhone (for iOS gate) | Signed build via macOS/Xcode |
| 8 | Host machine (Linux or Windows) running the Host Bridge | `go build ./cmd/host-bridge` |

## Step-by-step

### 1. Broker behind TLS

```bash
export DEVORBIT_ENV=production
export DEVORBIT_BROKER_SECRET="$(openssl rand -base64 48)"
export DEVORBIT_BROKER_PORT=8081
cd services/connection-broker && go run ./cmd/connection-broker &

# Caddy on the same host
export CADDY_EMAIL=ops@example.com BROKER_HOST=broker.example.com HOST_BRIDGE_HOST=host.example.com
caddy run --config infrastructure/deployment/Caddyfile --envfile infrastructure/deployment/.env
```

**Expected evidence**

```
curl -sS https://broker.example.com/api/v1/health
# -> {"status":"ok","service":"connection-broker",...}

curl -sSI https://broker.example.com/api/v1/health | grep -i strict-transport
# -> Strict-Transport-Security: max-age=31536000; includeSubDomains

curl -sS http://broker.example.com/api/v1/health
# -> HTTP 308 redirect to https (no cleartext service response)

openssl s_client -connect broker.example.com:443 -servername broker.example.com </dev/null 2>/dev/null \
  | openssl x509 -noout -issuer -dates
# -> issuer is a public CA, not self-signed, not expired
```

Record the output. `wss://` upgrade correctness is proven in Step 4.

### 2. Host Bridge in production mode behind the proxy

```bash
export DEVORBIT_ENV=production
export DEVORBIT_HOST_SECRET="$(openssl rand -base64 32)"
export DEVORBIT_ADVERTISED_URL=https://host.example.com
export DEVORBIT_BROKER_HTTP_URL=https://broker.example.com
export DEVORBIT_BROKER_WS_URL=wss://broker.example.com
export DEVORBIT_TRUSTED_PROXIES=127.0.0.1
export DEVORBIT_BIND_ADDR=127.0.0.1
export DEVORBIT_GEMINI_API_KEY=<host operator key>
cd agents/host-bridge && go run ./cmd/host-bridge
```

**Expected evidence**

- Startup logs show `listening on 127.0.0.1:8082 (Env: production …)` and a **pairing QR payload whose
  URLs are `https://`**.
- Negative check: start again with `DEVORBIT_ADVERTISED_URL=http://host.example.com` → process **must
  exit** with `must use https:// in production mode`.
- Negative check: unset `DEVORBIT_TRUSTED_PROXIES` and TLS files → process **must exit** with
  `production mode requires native TLS … or trusted proxies`.
- Untrusted forwarded header: `curl -sS -H 'X-Forwarded-Proto: https' http://<lan-ip>:8082/api/v1/sessions/revoke -X POST -d '{"session_id":"x"}'`
  from a non-proxy peer → **403** (confidential transport not satisfied).
- Multi-valued header rejection:
  `curl -sSk -H 'X-Forwarded-Proto: http' -H 'X-Forwarded-Proto: https' https://host.example.com/api/v1/pairing/challenge -X POST` → **403**.

### 3. Physical Android pairing + session

1. Install the app: `cd apps/mobile && flutter install --release` (device visible in `adb devices -l`).
2. Scan the startup QR code → app stores credentials in EncryptedSharedPreferences.
3. Observe host logs: `/api/v1/pairing/confirm` returns 200 and a session is provisioned; broker logs
   `Authenticated Client WSS connected`.
4. In the app, open **Projects** and load a workspace; run one AI prompt that requires approval and
   approve it.

**Expected evidence:** app screen recording + host log excerpt showing pairing, `session_ready`,
E2E request/response, approval consumption.

### 4. `wss://` relay over the internet (not LAN)

1. Disable Wi-Fi on the phone (use mobile data only).
2. Connect to the broker over `wss://broker.example.com`.
3. Repeat the workspace load and one prompt.

**Expected evidence:** broker access log shows the WebSocket upgrade over TLS (`101 Switching
Protocols`) and encrypted frame relay; host and device are on different networks.

### 5. Revocation + restart durability

1. On the host: `POST /api/v1/sessions/revoke` with the session id (host-admin credential).
2. App must show `DEVICE REVOKED`, clear stored credentials, and **not** reconnect.
3. Restart the Host Bridge; restart the app; attempt reconnect → must fail (no silent reactivation).
4. Restore a stale `host_http_sessions.json` snapshot and restart the host → the session must remain
   revoked (tombstone precedence).

### 6. HTTPS downgrade resistance

Point the QR payload at an `https://` URL whose certificate is invalid (e.g. self-signed). Pairing must
**fail closed** — the app must not fall back to `http://`.

### 7. iOS gate (separate, macOS required)

```bash
cd apps/mobile && flutter build ios --release --no-codesign
flutter build ipa --export-method app-store    # requires signing config
```

Install via TestFlight and repeat Steps 3–6. Expected: the local-network permission prompt appears
with the DevOrbit rationale (`NSLocalNetworkUsageDescription`), and LAN pairing succeeds while
internet traffic still requires TLS.

## Evidence table (fill in when executed)

| Step | Check | Result | Evidence reference | Date | Operator |
|---|---|---|---|---|---|
| 1 | Broker HTTPS + HSTS + CA issuer | PENDING | | | |
| 1 | Plain HTTP redirects (no cleartext) | PENDING | | | |
| 2 | Production config rejects `http://` advertised URL | PENDING | | | |
| 2 | Production config rejects missing TLS/trusted proxy | PENDING | | | |
| 2 | Untrusted `X-Forwarded-Proto` rejected (403) | PENDING | | | |
| 2 | Multi-valued `X-Forwarded-Proto` rejected (403) | PENDING | | | |
| 3 | Physical Android pairing + E2E task + approval | PENDING | | | |
| 4 | `wss://` relay across networks (mobile data) | PENDING | | | |
| 5 | Revocation, restart, stale-state replay | PENDING | | | |
| 6 | No HTTPS→HTTP downgrade on pairing | PENDING | | | |
| 7 | iOS build + TestFlight + Steps 3–6 | BLOCKED (needs macOS/Xcode) | | | |
