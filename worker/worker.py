#!/usr/bin/env python3
"""worker.py — JSONL bridge for Go Gateway <-> Python Core.

Reads JSON from stdin: {"task": "..."}
Streams JSONL events to stdout: {"type": "start|progress|done|error", ...}
"""
import json
import sys
import time


def main():
    req = json.loads(sys.stdin.read())
    task = req.get("task", "")

    # Emit start
    print(json.dumps({"type": "start", "payload": {"user_input": task}}), flush=True)

    # TODO: Call AgentFactory Core here
    # For now, emit mock events to test the renderer
    for i in range(3):
        print(json.dumps({
            "type": "progress",
            "payload": {"action": f"Step {i+1}/3", "progress": (i+1)/3.0}
        }), flush=True)
        time.sleep(0.1)

    # Emit done
    print(json.dumps({
        "type": "done",
        "payload": {"output": "**Task completed!**", "model": "qwen3.6-35b-a3b", "tokens": 1200, "elapsed_time": "3.2s"}
    }), flush=True)


if __name__ == "__main__":
    main()
