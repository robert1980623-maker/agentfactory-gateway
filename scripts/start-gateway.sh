#!/bin/bash
# AgentFactory Gateway — 启动脚本（确保单实例）
# 如果已有 Gateway 进程运行，先杀掉再启动新的。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GATEWAY_BIN="$PROJECT_DIR/bin/gateway"
PID_FILE="$PROJECT_DIR/gateway.pid"
LOG_DIR="$PROJECT_DIR/logs"
LOG_FILE="$LOG_DIR/gateway-$(date +%Y%m%d).log"

# ── 前置检查 ──
if [[ ! -x "$GATEWAY_BIN" ]]; then
    echo "ERROR: Gateway binary not found at $GATEWAY_BIN"
    echo "Run: cd $PROJECT_DIR && go build -o bin/gateway ."
    exit 1
fi

if [[ ! -f "$PROJECT_DIR/.env" ]]; then
    echo "ERROR: .env file not found at $PROJECT_DIR/.env"
    exit 1
fi

mkdir -p "$LOG_DIR"

# ── 单实例保护：杀掉已有进程 ──
if [[ -f "$PID_FILE" ]]; then
    OLD_PID=$(cat "$PID_FILE")
    if ps -p "$OLD_PID" > /dev/null 2>&1; then
        echo "[$(date +%H:%M:%S)] Killing existing Gateway process (PID: $OLD_PID)..."
        kill "$OLD_PID" 2>/dev/null || true
        sleep 2
        # 如果还在跑，强制杀掉
        if ps -p "$OLD_PID" > /dev/null 2>&1; then
            echo "[$(date +%H:%M:%S)] Force killing..."
            kill -9 "$OLD_PID" 2>/dev/null || true
            sleep 1
        fi
        echo "[$(date +%H:%M:%S)] Old process stopped."
    else
        echo "[$(date +%H:%M:%S)] PID file exists but process $OLD_PID is not running. Cleaning up."
        rm -f "$PID_FILE"
    fi
fi

# 二次确认：通过进程名查找，确保没有残留
EXISTING=$(pgrep -f "agentfactory.*bin/gateway" || true)
if [[ -n "$EXISTING" ]]; then
    echo "[$(date +%H:%M:%S)] Found residual Gateway processes: $EXISTING"
    echo "$EXISTING" | while read -r pid; do
        kill -9 "$pid" 2>/dev/null || true
    done
    sleep 1
    echo "[$(date +%H:%M:%S)] Residual processes cleaned."
fi

# ── 启动 Gateway ──
echo "[$(date +%H:%M:%S)] Starting AgentFactory Gateway..."
echo "  Binary:  $GATEWAY_BIN"
echo "  Working: $PROJECT_DIR"
echo "  Log:     $LOG_FILE"

cd "$PROJECT_DIR"
nohup ./bin/gateway >> "$LOG_FILE" 2>&1 &
NEW_PID=$!
echo "$NEW_PID" > "$PID_FILE"

# ── 验证启动 ──
sleep 3
if ps -p "$NEW_PID" > /dev/null 2>&1; then
    echo "[$(date +%H:%M:%S)] Gateway started successfully (PID: $NEW_PID)"
    echo "  Logs: tail -f $LOG_FILE"
    echo "  Stop:  kill $NEW_PID  (or run this script again)"
else
    echo "[$(date +%H:%M:%S)] ERROR: Gateway failed to start. Check logs:"
    tail -20 "$LOG_FILE"
    rm -f "$PID_FILE"
    exit 1
fi
