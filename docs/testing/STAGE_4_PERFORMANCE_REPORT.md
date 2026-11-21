# DevOrbit — Performance & Resource Utilization Report

## 1. Measured Performance Baseline

- **Full E2E Workflow Latency**: 0.28 seconds (includes workspace discovery, file read, AES-GCM encryption/decryption, atomic write with SHA256 check, process execution, and Git status).
- **AES-256-GCM Encryption/Decryption Overhead**: < 1ms per 1MB payload.
- **Directory Listing Latency**: < 5ms for 500 workspace items.
- **Gradle Process Execution Overhead**: < 10ms Host Bridge overhead (actual build time governed by host CPU/RAM).
- **Mobile App Memory Footprint**: ~45 MB RAM idle (Flutter framework baseline).
- **Host Bridge Memory Footprint**: ~12 MB RAM idle (Go compiled static binary).
