# ADR-0002: Agent Client Protocol (ACP) & AI Ecosystem Strategy

- **Status**: Accepted
- **Date**: 2026-11-21
- **Deciders**: Principal Architect, AI Integration Lead

## Context
DevOrbit connects developers to AI coding assistants (Google Antigravity, Gemini, OpenAI, Anthropic). A standard specification is required to interface with external coding agents without coupling the mobile UI directly to proprietary agent IPC mechanisms.

## Decision
1. **Agent Client Protocol (ACP) Adoption**: Adopt ACP as the primary standard protocol for interacting with host-side coding agents.
2. **ACP Adapter (`agents/acp-adapter`)**: Build a dedicated Go adapter package that translates DevOrbit JSON-RPC messages to stdio/socket ACP agent sessions.
3. **Direct AI Provider Router (`agents/ai-router`)**: For direct LLM API access (Gemini API, OpenAI API, Anthropic API), build modular provider adapters enforcing unified streaming, conversation history management, and token usage reporting.
4. **Approval Middleware**: All AI-generated file changes or host process commands must pass through an approval workflow gate requiring explicit mobile user confirmation before execution on the host.

## Consequences
- Official agents supporting ACP can be attached seamlessly without modifying mobile UI code.
- Direct model APIs provide reliable fallback when local agent sessions are unavailable.
- User retains explicit control over file modifications proposed by AI tools.
