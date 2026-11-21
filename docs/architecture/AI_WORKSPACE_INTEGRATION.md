# DevOrbit — AI Workspace & Human Approval Gate Specification

## 1. AI Integration Architecture

DevOrbit integrates Google Antigravity ACP agents and direct provider models (Gemini API, OpenAI API, Anthropic API) through the Host Bridge AI router (`agents/ai-router`).

```
Mobile AI Workspace UI
         |
    Prompt Input
         |
    AI Agent Processing
         |
   Tool Call Proposal (e.g. edit_file / execute_command)
         |
  AiToolApprovalDialog (Human Gate)
    +----+----+
    |         |
 Approve   Reject
    |         |
 Execute   Cancel
```

## 2. Human Approval Gate

- All AI-proposed file edits or host command executions MUST trigger the `AiToolApprovalDialog`.
- Displays proposed file path, action type, and diff preview.
- Requires explicit developer action (`Approve & Apply` or `Reject`) before any host modification occurs.
