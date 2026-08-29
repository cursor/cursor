# 🚀 Antigravity Gemini 3.7 & Exness MetaTrader 5 High Availability (HA) System

**Managed by:** `google.com.jules[bot]`
**Repository:** `https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206.git`
**Secondary Remote:** `https://github.com/Mouy-leng172/A6..9V-GenX_FX.main.git`

---

## ⚡ Single-Tap 24/7 VPS Deployment

Deploy 24/7 background execution with a single command:

### Option 1: Native Systemd Service / Script
```bash
./deploy-vps-247.sh
```

### Option 2: Docker Compose Container (Auto-Restart 24/7)
```bash
docker compose up -d
```

---

## 🛠️ Key Features & Architecture

1. **Modular EA Strategy Architecture:**
   - Entry Point Master Controller: `src/GeminiController.mq5`
   - Standalone Strategy EA: `src/GeminiBot.mq5`
   - Modular Strategies: `Strategy_MA.mqh`, `Strategy_Breakout.mqh`, `Strategy_TrailingStop.mqh`
   - Shared Utilities: `RiskManager.mqh` (Drawdown & Loss Protection), `Logger.mqh`

2. **Huawei Recycle Bin Auto-Adapter (`.RecycleBinHW`):**
   - File watcher automatically decompresses `.gzip` logs and auto-commits updates under `google.com.jules[bot]`.

3. **High Availability (HA) Control Server:**
   - REST API on Port `3000`: `/health`, `/migrate-ea`, `/start-mt5`, `/process-recyclebin`, `/sync-git`.

4. **Network Topology & Documentation:**
   - Full patch map in `NETWORK_PATCH_MAP.md`.
