# DevOrbit — Local Development Setup & Run Guide

This guide provides exact, copy-paste-ready instructions to spin up all DevOrbit services locally and connect an Android emulator or physical device.

---

## 1. System Components & Deployment Boundary Matrix

DevOrbit separates cloud control services from local workstation host daemons:

| Component | Port | Execution Model | Description |
|---|---|---|---|
| **PostgreSQL 16** | `5432` | Docker | Database for users, machines, and audit logs. |
| **Control Plane** | `8080` | Docker | Device registration & pairing token issuer. |
| **Connection Broker** | `8081` | Docker | Opaque WebSocket relay server for remote internet tunnel mode. |
| **Host Bridge Daemon** | `8082` | **Native Windows Host** | **MUST run natively on Windows host** to access the actual filesystem, Git CLI, Gradle Wrapper, and ADB devices. |
| **Flutter Mobile App** | N/A | Android / iOS / Emulator | Flutter client user interface. |

---

## 2. Environment Variables & Configuration Baseline

### A. Infrastructure (Docker Compose)
- `POSTGRES_USER`: `devorbit`
- `POSTGRES_PASSWORD`: `devorbit_password`
- `POSTGRES_DB`: `devorbit_db`
- `DATABASE_URL`: `postgres://devorbit:devorbit_password@postgres:5432/devorbit_db?sslmode=disable`
- `JWT_SECRET`: `devorbit_development_jwt_secret_key`

### B. Host Bridge Daemon (Windows Native Environment)
- `DEVORBIT_PORT`: `8082` (default)
- `DEVORBIT_MAX_FILE_SIZE_MB`: `10` (default)

---

## 3. Step-by-Step Local Launch Sequence

### STEP 1: Launch Infrastructure Services (Docker)
In PowerShell, navigate to the project root and launch PostgreSQL, Control Plane, and Connection Broker:

```powershell
docker compose -f infrastructure/docker/docker-compose.yml up -d
```

Verify container health:
```powershell
docker compose -f infrastructure/docker/docker-compose.yml ps
```

---

### STEP 2: Build & Launch Native Windows Host Bridge Daemon
The Host Bridge daemon **must run natively** on your Windows workstation so it has direct access to your local projects, Git, Gradle, and ADB:

```powershell
# Navigate to Host Bridge directory
cd agents/host-bridge

# Build native Windows executable
go build -o bin/host-bridge.exe ./cmd/host-bridge

# Run Host Bridge Daemon
.\bin\host-bridge.exe
```

*Terminal Output:*
```
2026/11/21 12:45:00 DevOrbit Host Bridge v1.2.1 listening on :8082 (Allowed Roots: 1)
```

---

### STEP 3: Verify Service Health
Open a new PowerShell window and run health checks:

```powershell
# Host Bridge Health Check
Invoke-RestMethod -Uri "http://localhost:8082/api/v1/health"

# Control Plane Health Check
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/auth/pair" -Method Post
```

---

### STEP 4: Launch Android Emulator & Connect App

#### Option A: Android Emulator (AVD)
1. Start the Pixel 8 emulator:
   ```powershell
   flutter emulators --launch Pixel_8
   ```

2. Run the Flutter mobile app targeting the emulator:
   ```powershell
   cd apps/mobile
   flutter run -d Pixel_8
   ```

3. **Networking Endpoint in Emulator**:
   - In Android Emulator, `127.0.0.1` refers to the emulator itself.
   - Use **`10.0.2.2:8082`** as the Host Bridge LAN address inside the emulator settings.

#### Option B: Physical Android Device (Over Local Wi-Fi)
1. Ensure your physical Android phone and Windows host are connected to the same Wi-Fi network.
2. Find your Windows host IP address:
   ```powershell
   (Get-NetIPAddress -AddressFamily IPv4 -InterfaceAlias "Wi-Fi*").IPAddress
   ```
   *(Example output: `192.168.1.100`)*

3. Enable USB Debugging on your phone, connect via USB, and launch:
   ```powershell
   cd apps/mobile
   flutter run -d <your-device-id>
   ```

4. **Networking Endpoint**: Enter **`192.168.1.100:8082`** in the DevOrbit Machine Settings screen.

---

### STEP 5: QR Device Pairing & Host Approval Procedure

1. In the mobile app, navigate to **Host Machines** -> tap **Pair New Host Machine (Scan QR)**.
2. The mobile app requests a single-use pairing challenge from `http://<host-ip>:8082/api/v1/pairing/challenge`.
3. The mobile app generates an HMAC-SHA256 proof-of-possession signature and submits it to `/api/v1/pairing/confirm`.
4. Upon successful verification, the Host Bridge issues a session token, and the machine card displays **Active / Connected**.

---

## 4. Shutdown & Cleanup Commands

To stop services safely:

```powershell
# Stop Docker containers
docker compose -f infrastructure/docker/docker-compose.yml down

# Stop Host Bridge in PowerShell: press Ctrl + C
```
