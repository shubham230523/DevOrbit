# DevOrbit — Git Workspace Integration Specification

## 1. Overview

The Git Workspace feature (`GitWorkspaceScreen`) provides read-only repository inspection using the Host Bridge Git client (`internal/infrastructure/git/client.go`).

## 2. Capabilities

- **Branch Indicator**: Displays current checked-out branch name (e.g. `main` or `feature/stage-3-mobile-workspace`).
- **Clean/Modified Badge**: Highlights working tree state.
- **Status Tree**: Categorizes files into Staged Changes (Green), Unstaged Changes (Orange), and Untracked Files (Blue).
- **Diff Inspector**: Renders `git diff HEAD` output text for review.
