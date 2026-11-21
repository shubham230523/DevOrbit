# DevOrbit — Remote Process Execution & Terminal Streaming

## 1. Process Lifecycle Management

Remote tasks (Gradle builds, unit tests, Git status, ADB device scans) execute on the workstation host under control of `agents/host-bridge/internal/infrastructure/process/runner.go`.

```
Queued -> Starting -> Running -> Completed / Failed / Cancelled
```

- **Binary Allowlisting**: Only allowlisted executables (`git`, `gradlew`, `./gradlew`, `gradle.bat`, `adb`) are executed. Shell chaining syntax (`;&|`$<>`) is strictly rejected.
- **Process Cancellation**: Tapping "Cancel Process" sends `POST /api/v1/processes/{id}/cancel` to issue a SIGKILL signal on the host.
- **Request-ID Idempotency**: Submitting tasks with a `request_id` prevents duplicate process executions upon network reconnection.

## 2. Output Log Streaming

- Standard output (`stdout`) and standard error (`stderr`) streams are buffered and transmitted over WebSocket frames.
- Mobile client (`RemoteTasksScreen`) renders colored terminal log lines with auto-scroll and pause functionality.
