# DevOrbit — Component Design Specification

## 1. Modular Clean Architecture

DevOrbit enforces Clean Architecture across both Go backend services/agents and the Flutter mobile application.

```
+-------------------------------------------------------+
| Presentation Layer / Adapters (UI, REST, WebSockets) |
+-------------------------------------------------------+
                           |
                           v
+-------------------------------------------------------+
| Use Cases / Application Logic (Services, Commands)    |
+-------------------------------------------------------+
                           |
                           v
+-------------------------------------------------------+
| Domain Layer (Entities, Interfaces, Value Objects)    |
+-------------------------------------------------------+
                           ^
                           |
+-------------------------------------------------------+
| Infrastructure (Git CLI, Gradle, Filesystem, DB)      |
+-------------------------------------------------------+
```

## 2. Host Bridge Design (`agents/host-bridge`)

### Packages
- `cmd/host-bridge`: Entry point, CLI flags, configuration loader.
- `internal/domain`: `Workspace`, `ProjectFile`, `GitStatus`, `BuildTask`, `Device`.
- `internal/filesystem`: File reader/writer, path sanitizer, allowlist jail guard.
- `internal/git`: Git CLI executor wrapper.
- `internal/gradle`: Gradle runner, task parser, build log streamer.
- `internal/adb`: Android SDK locator, ADB device scanner, logcat streamer.
- `internal/server`: HTTP/WebSocket server engine with TLS handler.

### Path Sanitizer & Sandbox Safeguards
```go
type PathSanitizer struct {
    AllowedRoots []string
}

func (s *PathSanitizer) ResolveAndValidate(requestedPath string) (string, error) {
    cleanPath := filepath.Clean(requestedPath)
    evalPath, err := filepath.EvalSymlinks(cleanPath)
    if err != nil {
        return "", fmt.Errorf("invalid path: %w", err)
    }

    for _, root := range s.AllowedRoots {
        evalRoot, _ := filepath.EvalSymlinks(root)
        if strings.HasPrefix(evalPath, evalRoot+string(filepath.Separator)) || evalPath == evalRoot {
            return evalPath, nil
        }
    }
    return "", errors.New("access denied: path outside allowed workspace boundaries")
}
```

## 3. Connection Broker Design (`services/connection-broker`)

### Session Tunnel Router
- Maintains concurrent connections map: `map[MachineID]*BridgeSession`.
- Routes frame packets from `MobileSessionID` to `BridgeSessionID`.
- Zero raw data disk persistence; memory-only framing buffer with strict rate limits.

## 4. Mobile Workspace Design (`apps/mobile`)

### State Management & Architecture
- Feature-first structure:
  - `lib/features/projects/`
  - `lib/features/editor/`
  - `lib/features/ai_workspace/`
  - `lib/features/machines/`
  - `lib/features/terminal/`
  - `lib/core/` (network, security, theme, storage)
- BLoC / StateNotifier clean unidirectional data flow.
- Repositories abstract local SQLite cache and remote WebSocket RPC data sources.
