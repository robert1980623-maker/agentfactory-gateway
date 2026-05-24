-- gateway_state.sql
-- Schema for Gateway task state (SQLite)
-- Replaces JSON file storage (gateway_state.json)

CREATE TABLE IF NOT EXISTS tasks (
    task_id    TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    slack_ts   TEXT NOT NULL,
    status     TEXT NOT NULL,  -- running, done, error, stopped
    updated_at TEXT NOT NULL   -- ISO 8601 timestamp
);

CREATE INDEX IF NOT EXISTS idx_tasks_status  ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_channel ON tasks(channel_id);
