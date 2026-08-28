# 🌐 Full Network Patch Map - Mouy-leng172 Infrastructure

<<<<<<< HEAD
**Owner / Maintainer:** Mouy-leng172
**Automation Bot:** `google.com.jules[bot]`
**Primary Repository:** `https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206.git`
**Secondary Remote:** `genx_fx` (`https://github.com/Mouy-leng172/A6..9V-GenX_FX.main.git`)
**Deployment Environment:** High Availability (HA) Windows Server Self-Hosting & VPS Node
=======
**Owner / Maintainer:** Mouy-leng172
**Automation Bot:** `google.com.jules[bot]`
**Primary Repository:** `https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206.git`
**Secondary Remote:** `genx_fx` (`https://github.com/Mouy-leng172/A6..9V-GenX_FX.main.git`)
**Deployment Environment:** High Availability (HA) Windows Server Self-Hosting & VPS Node
>>>>>>> origin/jules-7621746438528792519-9bcbbd70

---

## 🗺️ 1. Global Topology Overview

```
                        +---------------------------------------------+
                        |           GitHub Remote Repositories        |
                        |   - Mouy-leng172/Gemini-3.6-flash-Exness    |
                        |   - Mouy-leng172/A6..9V-GenX_FX.main        |
                        +----------------------+----------------------+
                                               |
                                     (Git Auto-Push/Pull Sync)
                                               v
                        +----------------------+----------------------+
                        |         Antigravity Gemini Node Server      |
                        |              (Port 3000 / HTTP API)         |
                        |  - Auto-Adapter: .RecycleBinHW File Watcher |
                        |  - Manager: google.com.jules[bot]           |
                        +----------+-----------------------+----------+
                                   |                       |
            (EA Compilation & Sync)|                       |(REST / API Balance Check)
                                   v                       v
      +----------------------------+----+     +------------+--------------------+
      |  MetaTrader 5 Live Terminal     |     |  Broker APIs & Trading VPS        |
      |  - Master EA: GeminiController   |     |  - Exness API ($20+ Auto Launch)   |
      |  - Single EA: GeminiBot.mq5      |     |  - FxPro Broker Endpoint          |
      |  - Strategies: MA, Breakout,     |     |  - GCS Log Sync (gsutil)          |
      |    Trailing Stop                 |     +----------------------------------+
      +---------------------------------+
```

---

## 🔌 2. Network Node & Port Mapping Table

| Node / Component | Host / IP / Port | Protocol / Path | Purpose & Description |
|---|---|---|---|
| **Antigravity Node Server** | `localhost:3000` | HTTP / REST | Main control server for Gemini 3.7 EA migration, status monitoring, and recycle bin processing. |
| **MetaTrader 5 HA Terminal** | Windows Server HA | IPC / Native RPC | Executes live trading commands via MT5 `terminal64.exe` (`/portable` mode). |
| **Exness Broker API** | Remote API | HTTPS | REST balance checks & trading execution trigger (Threshold: $\ge \$20.00$). |
| **FxPro Broker API** | Remote API | HTTPS | Secondary broker connectivity & balance auditing interface. |
| **Google Cloud Storage** | GCS Cloud | `gs://<bucket>/logs` | Automatic log archive node for execution & system performance logs. |
| **Firebase Hosting** | Firebase Edge CDN | HTTPS | Static website deployment & hosting portal. |

---

## 📑 3. HTTP Server Endpoints Route Map

<<<<<<< HEAD
- **GET `/health` / `/`**
  Returns system online status, Gemini model (`gemini-3.7-flash`), broker (`Exness`), HA node state, and bot manager (`google.com.jules[bot]`).
- **GET `/migrate-ea`**
  Triggers automated MetaEditor compilation for `src/GeminiController.mq5` and `src/GeminiBot.mq5`.
- **GET `/start-mt5`**
  Initiates MetaTrader 5 terminal process on Windows Server HA node.
- **GET `/process-recyclebin`**
  Scans `.RecycleBinHW`, decompresses Huawei log attachments (`.gzip`), adapts them, and commits/pushes via Git.
- **GET `/sync-git`**
=======
- **GET `/health` / `/`**
  Returns system online status, Gemini model (`gemini-3.7-flash`), broker (`Exness`), HA node state, and bot manager (`google.com.jules[bot]`).
- **GET `/migrate-ea`**
  Triggers automated MetaEditor compilation for `src/GeminiController.mq5` and `src/GeminiBot.mq5`.
- **GET `/start-mt5`**
  Initiates MetaTrader 5 terminal process on Windows Server HA node.
- **GET `/process-recyclebin`**
  Scans `.RecycleBinHW`, decompresses Huawei log attachments (`.gzip`), adapts them, and commits/pushes via Git.
- **GET `/sync-git`**
>>>>>>> origin/jules-7621746438528792519-9bcbbd70
  Executes automated Git pull/push workflow under `google-labs-jules[bot]`.

---

## 📦 4. MetaTrader 5 Expert Advisor Module Map

```
src/
├── GeminiController.mq5        <-- Master Orchestrator EA Entry Point
├── GeminiBot.mq5               <-- Standalone Moving Average EA
├── Logger.mqh                  <-- Shared File & Console Logging Component
├── RiskManager.mqh             <-- Drawdown & Loss Limit Risk Protection
├── Strategy_MA.mqh             <-- Exponential Moving Average Crossover Strategy
├── Strategy_Breakout.mqh       <-- Donchian Channel High/Low Breakout Strategy
└── Strategy_TrailingStop.mqh   <-- Dynamic Trailing Stop Execution Module
```

---

## 🔄 5. Automated Data Flow Pipeline

<<<<<<< HEAD
1. **File Upload Adaptation:**
   Uploads into `.RecycleBinHW` are picked up by `fs.watch()` in `scripts/node-gemini-server.js`, adapted/decompressed, and staged.
2. **Git Auto Sync:**
   `google.com.jules[bot]` automatically commits staged changes and synchronizes with GitHub main branch.
3. **Execution Lifecycle:**
=======
1. **File Upload Adaptation:**
   Uploads into `.RecycleBinHW` are picked up by `fs.watch()` in `scripts/node-gemini-server.js`, adapted/decompressed, and staged.
2. **Git Auto Sync:**
   `google.com.jules[bot]` automatically commits staged changes and synchronizes with GitHub main branch.
3. **Execution Lifecycle:**
>>>>>>> origin/jules-7621746438528792519-9bcbbd70
   `launch-all.sh` / `scripts/full-lifecycle.sh` runs environment setup, indices workspace, starts the Node server daemon, checks account balance ($\ge \$20$), and launches VPS trading operations.
