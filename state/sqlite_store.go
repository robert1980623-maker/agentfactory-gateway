package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const iso8601 = "2006-01-02T15:04:05Z"

// SQLiteStore implements task state persistence using SQLite.
type SQLiteStore struct {
	db     *sql.DB
	cancel context.CancelFunc
}

// NewSQLiteStore creates a new SQLiteStore and initialises the schema.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Connection pool: SQLite does not support concurrent writers.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Enable WAL mode for better concurrency.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// PRAGMA optimisations.
	pragmas := []string{
		"PRAGMA busy_timeout = 5000;",                // 5s wait for lock
		"PRAGMA synchronous = NORMAL;",               // balance safety and performance
		"PRAGMA wal_autocheckpoint = 1000;",          // WAL auto checkpoint
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %s: %w", p, err)
		}
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS task_records (
			task_id    TEXT PRIMARY KEY,
			channel_id TEXT,
			slack_ts   TEXT,
			user_id    TEXT,
			prompt     TEXT,
			status     TEXT,
			updated_at TEXT
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}

	// Indexes for common queries (ListActive, HasActiveTask, GetByChannel).
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_task_status ON task_records(status)",
		"CREATE INDEX IF NOT EXISTS idx_task_channel ON task_records(channel_id)",
		"CREATE INDEX IF NOT EXISTS idx_task_channel_status ON task_records(channel_id, status)",
	}
	for _, idx := range indexes {
		if _, err := db.Exec(idx); err != nil {
			db.Close()
			return nil, fmt.Errorf("create index: %w", err)
		}
	}

	// Start background WAL checkpoint goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	go checkpointLoop(ctx, db)

	return &SQLiteStore{db: db, cancel: cancel}, nil
}

// checkpointLoop runs wal_checkpoint(PASSIVE) every 5 minutes until ctx is cancelled.
func checkpointLoop(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		}
	}
}

// Set creates or replaces a task record. If UpdatedAt is zero, it is set to now.
func (s *SQLiteStore) Set(rec TaskRecord) error {
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO task_records (task_id, channel_id, slack_ts, user_id, prompt, status, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.TaskID,
		rec.ChannelID,
		rec.SlackTS,
		rec.UserID,
		rec.Prompt,
		rec.Status,
		rec.UpdatedAt.Format(iso8601),
	)
	if err != nil {
		return fmt.Errorf("set record: %w", err)
	}
	return nil
}

// Get returns a copy of a task's record, or nil/false if not found.
func (s *SQLiteStore) Get(taskID string) (*TaskRecord, bool) {
	var rec TaskRecord
	var updatedAt string
	err := s.db.QueryRow(
		`SELECT task_id, channel_id, slack_ts, user_id, prompt, status, updated_at
		 FROM task_records WHERE task_id = ?`,
		taskID,
	).Scan(&rec.TaskID, &rec.ChannelID, &rec.SlackTS, &rec.UserID, &rec.Prompt, &rec.Status, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	rec.UpdatedAt, _ = time.Parse(iso8601, updatedAt)
	return &rec, true
}

// ListActive returns all records with status "running".
func (s *SQLiteStore) ListActive() []*TaskRecord {
	rows, err := s.db.Query(
		`SELECT task_id, channel_id, slack_ts, user_id, prompt, status, updated_at
		 FROM task_records WHERE status = 'running'`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []*TaskRecord
	for rows.Next() {
		var rec TaskRecord
		var updatedAt string
		if err := rows.Scan(&rec.TaskID, &rec.ChannelID, &rec.SlackTS, &rec.UserID, &rec.Prompt, &rec.Status, &updatedAt); err != nil {
			continue
		}
		rec.UpdatedAt, _ = time.Parse(iso8601, updatedAt)
		result = append(result, &rec)
	}
	return result
}

// HasActiveTask returns true if the given channel has a running or paused task.
func (s *SQLiteStore) HasActiveTask(channelID string) bool {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM task_records
		 WHERE channel_id = ? AND (status = 'running' OR status = 'paused')`,
		channelID,
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// GetByChannel returns the most recent task record for a given channel,
// regardless of status. Useful for retry operations.
func (s *SQLiteStore) GetByChannel(channelID string) (*TaskRecord, bool) {
	var rec TaskRecord
	var updatedAt string
	err := s.db.QueryRow(
		`SELECT task_id, channel_id, slack_ts, user_id, prompt, status, updated_at
		 FROM task_records WHERE channel_id = ?
		 ORDER BY updated_at DESC LIMIT 1`,
		channelID,
	).Scan(&rec.TaskID, &rec.ChannelID, &rec.SlackTS, &rec.UserID, &rec.Prompt, &rec.Status, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	rec.UpdatedAt, _ = time.Parse(iso8601, updatedAt)
	return &rec, true
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
