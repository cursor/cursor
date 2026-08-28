#!/bin/bash
# ==============================================================================
# Single-Tap VPS 24/7 Deployment Script
# Managed by google.com.jules[bot] for Antigravity Gemini 3.7 & Exness MT5 HA
# ==============================================================================

set -e

echo "🚀 Starting 24/7 Single-Tap VPS Deployment..."

# 1. Environment Verification
if [ ! -f .env ]; then
    if [ -f .env.example ]; then
        echo "📄 Copying .env.example to .env..."
        cp .env.example .env
    else
        echo "⚠️ .env file not found. Creating default .env..."
        cat << 'ENVEOF' > .env
GITHUB_REPO=https://github.com/Mouy-leng172/Gemini-3.6-flash-Exness-f52ca206.git
BROKER=Exness
NODE_SERVER_PORT=3000
GEMINI_MODEL=gemini-3.7-flash
ENVEOF
    fi
fi

# 2. Create systemd 24/7 service unit if systemd is available
SERVICE_FILE="/etc/systemd/system/gemini-vps.service"

if [ -w /etc/systemd/system ] || command -v sudo &> /dev/null; then
    echo "⚙️ Configuring systemd 24/7 auto-restart service..."

    SYSTEMD_CONF="[Unit]
Description=Antigravity Gemini 3.7 & Exness MT5 24/7 Trading Service
After=network.target

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=$(pwd)
ExecStart=$(command -v node) $(pwd)/scripts/node-gemini-server.js
Restart=always
RestartSec=5
Environment=NODE_ENV=production

[Install]
WantedBy=multi-user.target"

    if [ -w /etc/systemd/system ]; then
        echo "$SYSTEMD_CONF" > $SERVICE_FILE
        systemctl daemon-reload
        systemctl enable gemini-vps
        systemctl restart gemini-vps
        echo "✅ Systemd service 'gemini-vps' enabled & started for 24/7 execution!"
    elif command -v sudo &> /dev/null; then
        echo "$SYSTEMD_CONF" | sudo tee $SERVICE_FILE > /dev/null
        sudo systemctl daemon-reload
        sudo systemctl enable gemini-vps
        sudo systemctl restart gemini-vps
        echo "✅ Systemd service 'gemini-vps' enabled & started for 24/7 execution via sudo!"
    fi
else
    echo "ℹ️ Systemd unavailable. Triggering daemon start script..."
    node scripts/node-gemini-server.js > logs/node-gemini-server.log 2>&1 &
    echo "✅ Server daemon started in background!"
fi

# 3. Trigger full lifecycle sequence
echo "🔄 Executing full lifecycle initialization..."
bash scripts/full-lifecycle.sh

echo "=========================================================================="
echo "🎉 SINGLE-TAP VPS 24/7 DEPLOYMENT COMPLETE!"
echo "Service Status Endpoint: http://localhost:3000/health"
echo "Managed by: google.com.jules[bot]"
echo "=========================================================================="
