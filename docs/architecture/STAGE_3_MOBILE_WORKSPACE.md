# DevOrbit — Stage 3 Mobile Workspace Architecture Specification

## 1. Executive Summary

Stage 3 completes DevOrbit's remote mobile workspace application for Android and iOS clients. The mobile app interfaces with desktop development host computers running the Go Host Bridge daemon via direct LAN mTLS or End-to-End encrypted Connection Broker tunnels.

All computational workloads (compilation, Gradle builds, Git status, ADB device inspection, static analysis, AI agent processing) execute on the workstation host machine. The mobile client provides a lightweight, responsive IDE interface.

## 2. Core Mobile Workspace Capabilities

```
                       DEVORBIT MOBILE APPLICATION
                                   |
    +-----------+------------+-----+------+------------+-----------+
    |           |            |            |            |           |
Dashboard   Explorer      Editor        Tasks         Git          AI
  Screen     Screen       Screen        Screen       Screen     Workspace
(Host state, (Tree nav,   (Syntax,     (Gradle,      (Branch,   (Antigravity
 LAN/WSS)   breadcrumbs) Save/Diff)   Terminal)     Diffs)     Tool Gate)
```

1. **Machine Dashboard (`features/dashboard`)**: Displays connected host details, latency, active transport mode (LAN mTLS vs Connection Broker WSS), and machine revocation actions.
2. **Workspace Discovery & Explorer (`features/projects`)**: Auto-discovers Android Studio, Gradle, and Git projects. Navigates nested directory structures with breadcrumb path bar and file search filter.
3. **Remote Code Editor (`features/editor`)**: Provides syntax highlighting for 12+ development languages, line numbering, find in file, and SHA256 base-hash conflict detection with a 2-way/3-way conflict resolution modal (`ConflictResolutionModal`).
4. **Remote Tasks & Streaming Logs (`features/tasks`)**: Triggers Gradle builds, unit tests, and ADB device scans with real-time stdout/stderr streaming logs, auto-scroll toggle, and process cancellation.
5. **Git Workspace Integration (`features/git`)**: Displays branch status, staged/unstaged/untracked file status tree, and `git diff HEAD` syntax viewer.
6. **AI Coding Workspace (`features/ai_workspace`)**: Integrates Google Antigravity ACP agents and direct provider models with an interactive Tool Call Proposal card and human approval gate (`AiToolApprovalDialog`).
