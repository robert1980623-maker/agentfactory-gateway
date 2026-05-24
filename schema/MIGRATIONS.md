# Gateway State Migration: JSON → SQLite

## Overview

Migrate Gateway task state persistence from JSON file storage to SQLite.

|                     | Before               | After                    |
|---------------------|----------------------|--------------------------|
| Storage             | `gateway_state.json` | `gateway_state.db`       |
| Format              | JSON map             | SQLite table (`tasks`)   |
| Concurrency         | `sync.RWMutex`       | SQLite WAL mode          |
| Schema              | Go struct only       | `schema/gateway_state.sql` |

## Why Migrate

1. **Crash recovery** — SQLite is atomic and journal-backed. A crash mid-write cannot corrupt the file like JSON can.
2. **Querying** — Indexes on `status` and `channel_id` enable efficient lookups (e.g. "all running tasks") without loading the entire state into memory.
3. **Concurrent access** — SQLite WAL mode supports multiple readers and a single writer safely, replacing the in-process mutex with a proper database lock.

## Migration Steps

### 1. Create the database

```bash
sqlite3 gateway_state.db < schema/gateway_state.sql
```

### 2. Export existing JSON state (if `gateway_state.json` exists)

The current JSON file stores a flat `map[string]TaskRecord`. Convert it to INSERT statements:

```bash
# Python one-liner to generate SQL inserts from JSON
python3 -c "
import json, sys
with open('gateway_state.json') as f:
    records = json.load(f)
for tid, r in records.items():
    ts = r['updated_at'].replace('T', ' ').rstrip('Z')
    print(f\"INSERT OR REPLACE INTO tasks VALUES ('{tid}', '{r['channel_id']}', '{r['slack_ts']}', '{r['status']}', '{ts}');\")
" > migration_inserts.sql
```

### 3. Run the migration

```bash
sqlite3 gateway_state.db < migration_inserts.sql
```

### 4. Verify

```bash
sqlite3 gateway_state.db "SELECT COUNT(*) FROM tasks;"
# Should match the number of keys in gateway_state.json
```

### 5. Switch over

Update the Go code to use the `sqlite` driver and `schema/gateway_state.sql` instead of `state_manager.go`'s JSON logic. Once verified, remove `gateway_state.json`.

## Schema Changelog

| Version | Date       | Change                                  |
|---------|------------|-----------------------------------------|
| 1       | 2026-05-24 | Initial schema: `tasks` table + indexes |
