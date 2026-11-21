# DevOrbit — Local Development Setup & Run Guide

This guide provides exact instructions to run DevOrbit locally.

---

## Setting Go Path in PowerShell

Go SDK is located at `C:\Users\shubham\go_sdk\go\bin`.

To permanently add Go to your user PATH in PowerShell (so it works in all new terminal windows):
```powershell
[System.Environment]::SetEnvironmentVariable("Path", $env:Path + ";C:\Users\shubham\go_sdk\go\bin", [System.EnvironmentVariableTarget]::User)
```

Or run `$env:Path += ";C:\Users\shubham\go_sdk\go\bin"` at the top of your terminal session.

---

## Native Go Launch (Recommended — No Docker Required!)

Launch each service in a PowerShell terminal:

### Terminal 1: Launch Host Bridge Daemon (Port 8082)
```powershell
$env:Path += ";C:\Users\shubham\go_sdk\go\bin"
cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\agents\host-bridge
go run ./cmd/host-bridge
```
*Listening on `:8082`*

### Terminal 2: Launch Control Plane Service (Port 8080)
```powershell
$env:Path += ";C:\Users\shubham\go_sdk\go\bin"
cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\services\control-plane
go run ./cmd/control-plane
```
*Listening on `:8080`*

### Terminal 3: Launch Connection Broker Service (Port 8081)
```powershell
$env:Path += ";C:\Users\shubham\go_sdk\go\bin"
cd C:\Users\shubham\Documents\PersonalProjects\DevOrbit\services\connection-broker
go run ./cmd/connection-broker
```
*Listening on `:8081`*

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
