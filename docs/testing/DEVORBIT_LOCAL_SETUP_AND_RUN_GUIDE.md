# DevOrbit — Local Development Setup & Run Guide

This guide provides exact instructions to run DevOrbit locally.

---

## Quick Diagnosis: Docker Desktop Error

If you see:
`open //./pipe/dockerDesktopLinuxEngine: The system cannot find the file specified`

This means **Docker Desktop is not running** on your Windows host. You have two options:
1. **Option 1 (Recommended & Easiest)**: Run all services **natively on Windows using Go** (no Docker required!).
2. **Option 2**: Start Docker Desktop from the Windows Start Menu and wait for the engine to initialize before running `docker compose`.

---

## Option 1: Pure Native Go Launch (No Docker Required!)

You can run all three Go services natively in separate PowerShell windows.

### Terminal 1: Launch Host Bridge Daemon (Port 8082)
```powershell
cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\agents\host-bridge
go run ./cmd/host-bridge
```
*Listening on `:8082`*

### Terminal 2: Launch Control Plane Service (Port 8080)
```powershell
cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\services\control-plane
go run ./cmd/control-plane
```
*Listening on `:8080`*

### Terminal 3: Launch Connection Broker Service (Port 8081)
```powershell
cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\services\connection-broker
go run ./cmd/connection-broker
```
*Listening on `:8081`*

---

## Option 2: Launching with Docker Desktop

If you prefer using Docker:
1. Open **Docker Desktop** from your Windows Start Menu.
2. Wait until the status in the bottom-left corner of Docker Desktop turns **green ("Engine running")**.
3. Run:
   ```powershell
   docker compose -f infrastructure/docker/docker-compose.yml up -d
   ```

---

## Launching Mobile App on Android Emulator

1. Launch Android Emulator (Pixel 8):
   ```powershell
   flutter emulators --launch Pixel_8
   ```

2. Run Flutter App:
   ```powershell
   cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\apps/mobile
   flutter run -d Pixel_8
   ```

3. **Networking Endpoint**:
   - In Android Emulator, enter **`10.0.2.2:8082`** as the Host Bridge address.
   - For a physical Android phone on the same Wi-Fi, enter your PC's local LAN IP (e.g. **`192.168.x.x:8082`**).

---

## Health Check Commands

In PowerShell:
```powershell
# Host Bridge Health Check
Invoke-RestMethod -Uri "http://localhost:8082/api/v1/health"

# Control Plane Health Check
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/auth/pair" -Method Post
```
