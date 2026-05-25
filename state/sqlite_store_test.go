package state

import (
	"os"
	"testing"
	"time"
)

// newTestStore creates a SQLiteStore backed by an in-memory database.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// 1. TestSQLiteNewStore creates a store and verifies table exists.
func TestSQLiteNewStore(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	// Verify table exists by inserting and retrieving a record.
	if err := store.Set(TaskRecord{TaskID: "t1", Status: "running"}); err != nil {
		t.Fatalf("Set after creation: %v", err)
	}
	rec, ok := store.Get("t1")
	if !ok || rec.TaskID != "t1" {
		t.Fatal("table should exist and be usable after creation")
	}
}

// 2. TestSQLiteSetAndGet verifies basic Set + Get roundtrip.
func TestSQLiteSetAndGet(t *testing.T) {
	store := newTestStore(t)

	rec := TaskRecord{
		TaskID:    "task-001",
		ChannelID: "C01ABC",
		SlackTS:   "1700000000.123456",
		UserID:    "U12345",
		Prompt:    "Write a Go function",
		Status:    "running",
	}
	if err := store.Set(rec); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, ok := store.Get("task-001")
	if !ok {
		t.Fatal("expected record after Set")
	}
	if got.TaskID != "task-001" {
		t.Errorf("TaskID: got %q, want task-001", got.TaskID)
	}
	if got.ChannelID != "C01ABC" {
		t.Errorf("ChannelID: got %q, want C01ABC", got.ChannelID)
	}
	if got.SlackTS != "1700000000.123456" {
		t.Errorf("SlackTS: got %q, want 1700000000.123456", got.SlackTS)
	}
	if got.UserID != "U12345" {
		t.Errorf("UserID: got %q, want U12345", got.UserID)
	}
	if got.Prompt != "Write a Go function" {
		t.Errorf("Prompt: got %q", got.Prompt)
	}
	if got.Status != "running" {
		t.Errorf("Status: got %q, want running", got.Status)
	}
}

// 3. TestSQLiteGetNotFound returns false for missing task.
func TestSQLiteGetNotFound(t *testing.T) {
	store := newTestStore(t)
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("Get should return false for nonexistent task")
	}
}

// 4. TestSQLiteSetZeroUpdatedAt sets UpdatedAt when it is zero.
func TestSQLiteSetZeroUpdatedAt(t *testing.T) {
	store := newTestStore(t)

	rec := TaskRecord{TaskID: "t1", Status: "running"}
	// UpdatedAt is zero.
	if err := store.Set(rec); err != nil {
		t.Fatal(err)
	}
	got, ok := store.Get("t1")
	if !ok {
		t.Fatal("expected record")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set automatically when zero")
	}
	if got.UpdatedAt.After(time.Now().UTC()) {
		t.Error("UpdatedAt should not be in the future")
	}
}

// 5. TestSQLiteSetOverwrite verifies INSERT OR REPLACE replaces the full record.
func TestSQLiteSetOverwrite(t *testing.T) {
	store := newTestStore(t)

	// Insert a full record.
	store.Set(TaskRecord{
		TaskID:    "t1",
		ChannelID: "C01",
		SlackTS:   "ts1",
		UserID:    "U1",
		Prompt:    "old prompt",
		Status:    "running",
		UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	// Replace with new values.
	store.Set(TaskRecord{
		TaskID:    "t1",
		ChannelID: "C02",
		SlackTS:   "ts2",
		UserID:    "U2",
		Prompt:    "new prompt",
		Status:    "done",
		UpdatedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	})

	got, ok := store.Get("t1")
	if !ok {
		t.Fatal("expected record after overwrite")
	}
	if got.ChannelID != "C02" {
		t.Errorf("ChannelID: got %q, want C02", got.ChannelID)
	}
	if got.Prompt != "new prompt" {
		t.Errorf("Prompt: got %q, want 'new prompt'", got.Prompt)
	}
	if got.Status != "done" {
		t.Errorf("Status: got %q, want done", got.Status)
	}
}

// 6. TestSQLiteListActive returns only running tasks.
func TestSQLiteListActive(t *testing.T) {
	store := newTestStore(t)

	store.Set(TaskRecord{TaskID: "t1", Status: "running"})
	store.Set(TaskRecord{TaskID: "t2", Status: "done"})
	store.Set(TaskRecord{TaskID: "t3", Status: "running"})
	store.Set(TaskRecord{TaskID: "t4", Status: "error"})
	store.Set(TaskRecord{TaskID: "t5", Status: "paused"})

	active := store.ListActive()
	if len(active) != 2 {
		t.Fatalf("ListActive: got %d records, want 2", len(active))
	}

	ids := make(map[string]bool)
	for _, r := range active {
		ids[r.TaskID] = true
	}
	if !ids["t1"] || !ids["t3"] {
		t.Errorf("ListActive: expected t1 and t3, got %v", ids)
	}
}

// 7. TestSQLiteListActiveEmpty returns empty slice when no running tasks.
func TestSQLiteListActiveEmpty(t *testing.T) {
	store := newTestStore(t)
	store.Set(TaskRecord{TaskID: "t1", Status: "done"})
	store.Set(TaskRecord{TaskID: "t2", Status: "error"})

	active := store.ListActive()
	if len(active) != 0 {
		t.Errorf("ListActive: expected 0, got %d", len(active))
	}
}

// 8. TestSQLiteHasActiveTaskRunning detects running tasks.
func TestSQLiteHasActiveTaskRunning(t *testing.T) {
	store := newTestStore(t)
	store.Set(TaskRecord{TaskID: "t1", ChannelID: "C01", Status: "running"})

	if !store.HasActiveTask("C01") {
		t.Error("HasActiveTask should be true for running task")
	}
	if store.HasActiveTask("C02") {
		t.Error("HasActiveTask should be false for different channel")
	}
}

// 9. TestSQLiteHasActiveTaskPaused detects paused tasks.
func TestSQLiteHasActiveTaskPaused(t *testing.T) {
	store := newTestStore(t)
	store.Set(TaskRecord{TaskID: "t1", ChannelID: "C01", Status: "paused"})

	if !store.HasActiveTask("C01") {
		t.Error("HasActiveTask should be true for paused task")
	}
}

// 10. TestSQLiteHasActiveTaskDoneIsNotActive returns false for done/error tasks.
func TestSQLiteHasActiveTaskDoneIsNotActive(t *testing.T) {
	store := newTestStore(t)
	store.Set(TaskRecord{TaskID: "t1", ChannelID: "C01", Status: "done"})
	store.Set(TaskRecord{TaskID: "t2", ChannelID: "C02", Status: "error"})

	if store.HasActiveTask("C01") {
		t.Error("HasActiveTask should be false for done task")
	}
	if store.HasActiveTask("C02") {
		t.Error("HasActiveTask should be false for error task")
	}
}

// 11. TestSQLiteGetByChannel returns the most recent record.
func TestSQLiteGetByChannel(t *testing.T) {
	store := newTestStore(t)

	store.Set(TaskRecord{
		TaskID:    "t1",
		ChannelID: "C01",
		Prompt:    "first",
		UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	store.Set(TaskRecord{
		TaskID:    "t2",
		ChannelID: "C01",
		Prompt:    "second",
		UpdatedAt: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	})
	store.Set(TaskRecord{
		TaskID:    "t3",
		ChannelID: "C02",
		Prompt:    "other channel",
		UpdatedAt: time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
	})

	got, ok := store.GetByChannel("C01")
	if !ok {
		t.Fatal("expected record for C01")
	}
	if got.TaskID != "t2" {
		t.Errorf("GetByChannel(C01): got %q, want t2", got.TaskID)
	}
	if got.Prompt != "second" {
		t.Errorf("Prompt: got %q, want 'second'", got.Prompt)
	}
}

// 12. TestSQLiteGetByChannelNotFound returns false for unknown channel.
func TestSQLiteGetByChannelNotFound(t *testing.T) {
	store := newTestStore(t)
	_, ok := store.GetByChannel("C999")
	if ok {
		t.Error("GetByChannel should return false for unknown channel")
	}
}

// 13. TestSQLiteClose verifies the store can be closed without error.
func TestSQLiteClose(t *testing.T) {
	store := newTestStore(t)
	if err := store.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Closing again should be safe (sql.DB is safe for multiple closes).
	if err := store.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// 14. TestSQLiteFileBackedStore verifies persistence to a file on disk.
func TestSQLiteFileBackedStore(t *testing.T) {
	path := t.TempDir() + "/test.db"

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(file): %v", err)
	}

	store.Set(TaskRecord{
		TaskID:    "t1",
		ChannelID: "C01",
		Status:    "running",
		Prompt:    "persist me",
	})
	store.Close()

	// Verify file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("database file should exist on disk")
	}

	// Reopen and verify data persists.
	store2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen): %v", err)
	}
	defer store2.Close()

	got, ok := store2.Get("t1")
	if !ok {
		t.Fatal("expected record after reopen")
	}
	if got.Status != "running" {
		t.Errorf("Status after reopen: got %q, want running", got.Status)
	}
	if got.Prompt != "persist me" {
		t.Errorf("Prompt after reopen: got %q", got.Prompt)
	}
}

// 15. TestSQLiteMultipleChannels verifies operations across multiple channels.
func TestSQLiteMultipleChannels(t *testing.T) {
	store := newTestStore(t)

	store.Set(TaskRecord{TaskID: "t1", ChannelID: "C01", Status: "running"})
	store.Set(TaskRecord{TaskID: "t2", ChannelID: "C02", Status: "running"})
	store.Set(TaskRecord{TaskID: "t3", ChannelID: "C03", Status: "done"})

	active := store.ListActive()
	if len(active) != 2 {
		t.Fatalf("expected 2 active, got %d", len(active))
	}

	if !store.HasActiveTask("C01") {
		t.Error("C01 should have active task")
	}
	if !store.HasActiveTask("C02") {
		t.Error("C02 should have active task")
	}
	if store.HasActiveTask("C03") {
		t.Error("C03 should NOT have active task (done)")
	}

	r1, ok := store.GetByChannel("C01")
	if !ok || r1.TaskID != "t1" {
		t.Error("GetByChannel(C01) should return t1")
	}
	r2, ok := store.GetByChannel("C02")
	if !ok || r2.TaskID != "t2" {
		t.Error("GetByChannel(C02) should return t2")
	}
	r3, ok := store.GetByChannel("C03")
	if !ok || r3.TaskID != "t3" {
		t.Error("GetByChannel(C03) should return t3")
	}
}
