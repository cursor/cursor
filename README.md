# Gemini 3.6 Flash / 3.7 Exness Project

Automated MetaTrader 5 Expert Advisor & Node Server Management System for Exness Live Trading on Windows Server Self-Hosting High Availability (HA) Infrastructure.

## 🚀 Features & Components

- **Modular EA Architecture:** Master EA (`src/GeminiController.mq5`) controlling pluggable strategy modules (`Strategy_MA.mqh`, `Strategy_Breakout.mqh`, `Strategy_TrailingStop.mqh`).
- **Antigravity Node Server:** Server (`scripts/node-gemini-server.js`) providing HTTP endpoints for EA migration, MT5 status monitoring, and auto-sync.
- **Huawei Recycle Bin Auto-Adapter:** Automatic watcher for `.RecycleBinHW` files with `gzip` log decompression and auto-staging.
- **Git Auto-Sync Workflow:** Automated push/pull operations managed by `google.com.jules[bot]`.
- **Full Lifecycle Script:** Executable pipeline (`scripts/full-lifecycle.sh` and `launch-all.sh`) for automated build, balance checks, and production launch.

## 🌐 Network Patch Map

For a full map of network nodes, ports, endpoints, and data flow architecture, see **[NETWORK_PATCH_MAP.md](NETWORK_PATCH_MAP.md)**.
