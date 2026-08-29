#!/bin/bash
# Test script for Go SDK (when Go is available)

set -e

echo "=== Testing Go SDK ==="
echo

# Check prerequisites
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed. Please install Go 1.24 or later."
    echo "   Visit: https://golang.org/dl/"
    exit 1
fi

# Determine COPILOT_CLI_PATH
if [ -z "$COPILOT_CLI_PATH" ]; then
    # Try to find it relative to the SDK. As of CLI 1.0.64-1 the @github/copilot
    # package is a thin loader; the runnable index.js ships in the installed
    # platform package (e.g. @github/copilot-linux-x64). Exactly one is installed.
    SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
    POTENTIAL_PATH="$(ls "$SCRIPT_DIR"/../nodejs/node_modules/@github/copilot-*/index.js 2>/dev/null | head -n1)"
    if [ -n "$POTENTIAL_PATH" ] && [ -f "$POTENTIAL_PATH" ]; then
        export COPILOT_CLI_PATH="$POTENTIAL_PATH"
        echo "📍 Auto-detected CLI path: $COPILOT_CLI_PATH"
    else
        echo "❌ COPILOT_CLI_PATH environment variable not set"
        echo "   Run: export COPILOT_CLI_PATH=/path/to/dist-cli/index.js"
        exit 1
    fi
fi

if [ ! -f "$COPILOT_CLI_PATH" ]; then
    echo "❌ CLI not found at: $COPILOT_CLI_PATH"
    exit 1
fi

echo "✅ Go version: $(go version)"
echo "✅ CLI path: $COPILOT_CLI_PATH"
echo

# Run Go tests
cd "$(dirname "$0")"

echo "=== Running Go SDK E2E Tests ==="
echo

go test -v ./... -race -timeout=20m

echo
echo "✅ All tests passed!"
