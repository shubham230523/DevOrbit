# DevOrbit — Gemini API Configuration & Key Provisioning Guide

This document specifies the host-only credential provisioning model and mock configuration options for Gemini API integration in DevOrbit.

---

## 1. Host-Only Credential Provisioning Model

To ensure security and eliminate key leakage risks:

- **GEMINI_API_KEY is provisioned exclusively on the development host computer** via environment variables (`GEMINI_API_KEY` or `DEVORBIT_GEMINI_API_KEY`).
- **Mobile devices NEVER store or transmit API keys**. Mobile devices communicate with the host via E2E-encrypted WebSocket frames, sending prompt text and tool approvals while receiving streamed response text and tool execution logs.

---

## 2. Environment Variables Matrix

| Environment Variable | Allowed Values | Description |
|---|---|---|
| `GEMINI_API_KEY` | String e.g. `AIzaSy...` | Primary Gemini API key provisioned on host computer. |
| `DEVORBIT_GEMINI_API_KEY` | String e.g. `AIzaSy...` | Fallback environment variable for Gemini API key. |
| `DEVORBIT_GEMINI_USE_MOCK` | `true` \| `false` | Explicitly enables `MockProvider` for offline development/testing. Default is `false`. |
| `DEVORBIT_ENV` | `development` \| `production` | Production mode enforces non-empty `GEMINI_API_KEY` and rejects unauthenticated mock usage. |

---

## 3. Provider Resolution & Error Behavior

1. **Production Mode (`DEVORBIT_ENV=production`)**:
   - Requires valid `GEMINI_API_KEY`.
   - If `GEMINI_API_KEY` is missing, generation calls return HTTP **503 Service Unavailable** (`ErrProviderUnavailable`):
     `503 Service Unavailable: GEMINI_API_KEY is not configured on the host`
   - No silent fallback to mock providers occurs in production.

2. **Testing / Mock Mode**:
   - Set `DEVORBIT_GEMINI_USE_MOCK=true` to enable offline mock responses for unit testing and offline development.
