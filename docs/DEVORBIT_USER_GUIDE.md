# DevOrbit — Simple Mobile User & Reference Guide

This guide explains how to set up, pair, and use the **DevOrbit** mobile application to control your home or office development computer remotely, execute AI coding tasks with Gemini, and manage workspace files.

---

## 1. What is DevOrbit?

**DevOrbit** turns your phone into a remote control cockpit for AI-assisted software development:
- **Gemini AI** is the intelligence engine.
- **Your Workstation Computer** is the execution environment where files, builds, and tools run.
- **DevOrbit Mobile** is your remote control interface.

You can travel or work away from your desk while securely inspecting code, submitting instructions to Gemini AI, approving file edits, and running builds on your home computer over 5G/LTE or external Wi-Fi networks.

---

## 2. Quick Navigation Bar & Tab Summary

DevOrbit Mobile features a dark-themed 5-tab bottom navigation bar designed for easy reach on mobile screens:

| Tab Name | Icon | Core Function |
|---|---|---|
| **`Home`** | 📱 | Displays host connection status, connection broker WSS endpoint input, and host pairing status. |
| **`Travel AI`** | ✨ | **Primary AI Coding Cockpit**: Chat with Gemini AI, select host projects, stream tool logs, and review/approve file edits. |
| **`Projects`** | 📁 | **Live File Explorer**: Browse folders and files stored on your desktop computer in real-time. |
| **`Machines`** | 💻 | **Connection Manager**: Switch between direct LAN (mTLS) and Cloud Broker (WSS) modes, or pair new host computers. |
| **`Settings`** | ⚙️ | **Preferences**: Toggle biometric authentication, AI tool execution confirmations, or clear paired credentials. |

---

## 3. How to Launch Services & Connect (Connection Broker + Host Bridge)

Before your mobile phone can connect via WSS broker, ensure both services are running on your computer:

### Step A: Launch Connection Broker (Port 8081)
In terminal / PowerShell:
```powershell
Set-Location services/connection-broker
go run ./cmd/connection-broker
```
*(Listening on `:8081`)*

### Step B: Launch Host Bridge (Port 8082)
In a second terminal window:
```powershell
Set-Location agents/host-bridge
$env:DEVORBIT_GEMINI_API_KEY="AIzaSy..."  # (Optional for Gemini AI)
go run ./cmd/host-bridge
```
*(Listening on `:8082`)*

### Step C: Network / USB Connection (Automatic LAN vs USB `adb reverse`)
- **Same Wi-Fi / LAN (Recommended — No `adb reverse` needed)**:
  When your phone and computer are on the same Wi-Fi network, Host Bridge and DevOrbit Mobile automatically resolve your computer's LAN IP for both pairing (`:8082`) and the Connection Broker (`:8081`). Just make sure Windows Firewall allows inbound TCP on ports `8081` and `8082` on your Private network.
- **USB-Only Testing (`ws://127.0.0.1:8081`)**:
  If you are connecting over USB via `127.0.0.1`, run this command in terminal:
  ```bash
  adb reverse tcp:8081 tcp:8081; adb reverse tcp:8082 tcp:8082
  ```
  *(This forwards `127.0.0.1:8081` and `127.0.0.1:8082` on your phone to your PC over USB. Note that `adb reverse` resets whenever the USB cable is unplugged, the phone reboots, or ADB restarts.)*

> **Console Output & Visual Terminal QR Code**:
> When the Host Bridge starts, it renders a **visual 2D QR Code image** directly in your terminal using Unicode block characters, as well as the JSON payload:
> ```text
> ================================================================================
>                 DEVORBIT HOST BRIDGE — PAIRING QR CODE & PAYLOAD
> ================================================================================
> Scan this QR Code with your phone camera in DevOrbit Mobile:
>
> ██████████████████████████████████████████████████
> ██████████████████████████████████████████████████
> ████      ████  ██    ██    ██  ██████    ████████
> ████  ██  ████  ████  ██  ████      ██  ████  ████
> ████  ██  ████  ████  ██  ██████  ████  ████  ████
> ...
>
> Or copy/paste this JSON payload manually (Machines -> Pair Host):
>
> {
>   "v": "1.0",
>   "challenge_id": "ch-...",
>   "host_id": "host-desktop-...",
>   "host_name": "Desktop Host",
>   "pairing_payload": "...",
>   "confirm_url": "http://10.118.3.69:8082/api/v1/pairing/confirm"
> }
> ================================================================================
> 2026/11/23 21:43:12 DevOrbit Host Bridge v1.2.1 listening on :8082
> ```

---

## 4. Step-by-Step Usage Instructions

### Step 1: Pair Your Phone with Your Computer (One-Time Setup)
1. Launch the **Host Bridge** daemon on your workstation computer (as shown above).
2. On your mobile phone, open DevOrbit and go to **`Machines`** → tap **`Pair Host Machine (Scan QR)`**.
3. Point your phone camera at the **terminal screen QR Code** to scan it instantly!
   *(Or tap "Enter Payload Manually" and paste the JSON payload).*
4. Your phone and computer are now cryptographically paired via HKDF-SHA256 E2E encryption!

---

### Step 2: Submit AI Coding Tasks (**`Travel AI`** Tab)
1. Tap the **`Travel AI`** tab at the bottom.
2. In the top bar, select your host project from the **Project Dropdown** (e.g., `DevOrbit`).
3. Select your preferred Gemini AI model (`Gemini 2.5 Flash`, `Gemini 2.5 Pro`, or `Gemini 3.8 Flash`).
4. Type your prompt in plain English (e.g., *"Add input validation to the pairing payload parser"*).
5. Tap **Send**. Gemini will analyze your project files on your computer and generate the required changes.

---

### Step 3: Review & Approve Code Changes (Human Safety Gate)
1. Whenever Gemini proposes mutating operations (`create_file`, `edit_file`, `delete_file`, or `execute_command`), an **Approval Proposal** popup appears on your phone.
2. Tap **View Diff** to inspect the exact lines added or deleted.
3. Tap **[Approve]**:
   - DevOrbit verifies the file's base SHA256 hash to prevent race conditions and applies the atomic edit directly on your computer.
4. Tap **[Reject]**:
   - The task is halted immediately. **No changes are made to your computer.**

---

### Step 4: Browse & Edit Workspace Files (**`Projects`** Tab)
1. Tap the **`Projects`** tab to view your desktop project folders in real-time.
2. Tap any file to open it in the mobile code editor.
3. Make quick edits if needed and tap **Save to Host** (uses atomic SHA256 verification).

---

### Step 5: Execute Terminal Commands & Builds (**`Tasks`** Screen)
1. To run builds or tests remotely, open the **Tasks & Terminal** screen.
2. Tap **`Gradle Build`**, **`Gradle Test`**, or **`ADB Devices`**.
3. Watch live stdout, stderr, and build logs stream directly to your phone terminal!

---

## 4. Troubleshooting & Connection Tips

### Android Studio Device Discovery
- **Using USB Cable**: Plug in your phone via USB, enable **USB Debugging** on the phone, and select your phone name (`vivo V2575`) in Android Studio's top toolbar device selector.
- **Using Wireless ADB**: If `flutter devices` reports an mDNS name error (e.g., `(2)._adb-tls-connect...`), open terminal in Android Studio (`Alt + F12`) and run:
  ```bash
  adb disconnect
  adb connect <YOUR_PHONE_IP>:<YOUR_PORT>
  ```
  Then click **Restart Flutter Daemon** in Android Studio's device dropdown.

### Network Modes
- **LAN (mTLS) Mode**: Use when both your phone and workstation are connected to the same local Wi-Fi network.
- **Broker (WSS) Mode**: Use when traveling away from home (on mobile 5G/LTE data or external Wi-Fi). The connection broker relays E2E encrypted messages without requiring opened firewall ports on your home computer.

---

## 5. Theme & UI Styling Reference

- **Dark Theme Surface**: `#1E1E1E` (AppBar, Cards, Navigation Bar)
- **Scaffold Background**: `#121212`
- **Accent Color**: `#BB86FC` (Purple Accent)
- **Navigation Bar**: Custom `#1E1E1E` background with `#2C2C2C` top border line to seamlessly match dark cards and app bar.
