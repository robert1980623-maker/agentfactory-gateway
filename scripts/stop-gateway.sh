#!/bin/bash
# AgentFactory Gateway — 停止脚本

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PID_FILE="$PROJECT_DIR/gateway.pid"

if [[ ! -f "$PID_FILE" ]]; then
    echo "No PID file found. Checking for running processes..."
    EXISTING=$(pgrep -f "agentfactory.*bin/gateway" || true)
    if [[ -n "$EXISTING" ]]; then
        echo "Found Gateway processes: $EXISTING"
        echo "$EXISTING" | while read -r pid; do
            kill "$pid" 2>/dev/null || true
        done
        echo "Stopped."
    else
        echo "No Gateway process found."
    fi
    exit 0
fi

PID=$(cat "$PID_FILE")
if ps -p "$PID" > /dev/null 2>&1; then
    echo "Stopping Gateway (PID: $PID)..."
    kill "$PID" 2>/dev/null || true
    sleep 2
    if ps -p "$PID" > /dev/null 2>&1; then
        echo "Force killing..."
        kill -9 "$PID" 2>/dev/null || true
    fi
    echo "Stopped."
else
    echo "Gateway process $PID is not running."
fi

rm -f "$PID_FILE"
