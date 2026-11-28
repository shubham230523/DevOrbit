# DevOrbit Deployment (TLS / production baseline)

Repository-side prerequisites for a production deployment. **Configuring a proxy does not prove
production TLS** — certificate issuance, DNS, and physical clients must be validated on a real host
(see [LIVE_TLS_VALIDATION_PROCEDURE.md](../../docs/testing/LIVE_TLS_VALIDATION_PROCEDURE.md)).

## 1. Components and ports

| Component | Default port | Exposure | Notes |
|---|---|---|---|
| Connection Broker | 8081 | Public via TLS proxy (`wss://` + `https://`) | Blind E2E relay; holds session authorization state |
| Host Bridge | 8082 | Loopback behind proxy, or LAN (dev only) | Must be reached over `https://` in production |
| Control Plane | 8080 | **Not deployed** — legacy prototype, unauthenticated | Do not expose; see KNOWN_ISSUES.md |
| PostgreSQL | 5432 | Internal only | Not yet used by any Go service (assessed in MP25 Part 4) |

## 2. TLS termination

`Caddyfile` obtains and renews CA-trusted certificates automatically and proxies:

- `/ws/*` → broker, with buffering disabled and read/write timeouts removed (long-lived WebSocket tunnels).
- `/api/v1/*` → broker, with a 15 s response-header timeout and health checks.
- Host Bridge host → loopback upstream with exactly one `X-Forwarded-Proto` value.

Security headers (HSTS, `X-Content-Type-Options`, `Referrer-Policy`, `COOP`) are set at the edge.

### Upstream confidentiality rules

- The broker speaks plaintext HTTP to loopback only. Never bind it to a public interface.
- The Host Bridge treats `X-Forwarded-Proto` as authoritative **only** when the immediate peer is in
  `DEVORBIT_TRUSTED_PROXIES` **and** exactly one such header exists (`auth_middleware.go`).
  Configure `DEVORBIT_TRUSTED_PROXIES=127.0.0.1` when Caddy runs on the same host.
- Production mode refuses to start without native TLS *or* trusted proxies, and requires
  `DEVORBIT_ADVERTISED_URL=https://…` (`internal/config/config.go`).

## 3. Configuration

```bash
cp infrastructure/deployment/env.example infrastructure/deployment/.env
# fill in values; .env is git-ignored (*.env)
```

Secrets (broker admin secret, per-host secrets, provider API keys) belong in a secret manager and
should be injected as environment variables at runtime. The committed `docker-compose.yml` in
`infrastructure/docker/` is a **development** file and contains a placeholder database password; it
is not a production deployment definition.

## 4. Health checks and graceful shutdown

| Service | Liveness/readiness endpoint |
|---|---|
| Broker | `GET /api/v1/health` |
| Host Bridge | `GET /api/v1/health` |
| Control Plane (unused) | `GET /api/v1/health` |

Caddy probes each upstream with `health_uri` and will stop routing to an unhealthy upstream. The Host
Bridge drains for 5 s on `SIGTERM` before exiting, so a rolling restart does not abruptly drop
in-flight requests; clients reconnect through the broker and re-authenticate.

## 5. What remains blocked

| Requirement | Status | Why |
|---|---|---|
| CA-trusted certificate issuance | **BLOCKED** | Needs a real DNS name pointing at a reachable host |
| End-to-end `wss://` relay validation | **BLOCKED** | Needs broker + host + physical device on real networks |
| Physical Android/iOS pairing over TLS | **BLOCKED** | No devices attached to the implementation host |
| Load/soak validation | **NOT IMPLEMENTED** | Deferred to M5 |
