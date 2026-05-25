package state

// StateManager defines the interface for task state persistence.
// Both JSON-backed StateManager and SQLite-backed SQLiteStore implement this.
type StateManager interface {
	Set(rec TaskRecord) error
	Get(taskID string) (*TaskRecord, bool)
	ListActive() []*TaskRecord
	HasActiveTask(channelID string) bool
	GetByChannel(channelID string) (*TaskRecord, bool)
}

// ClosableStateManager extends StateManager with a Close method.
// SQLiteStore implements this; JSON StateManager does not.
type ClosableStateManager interface {
	StateManager
	Close() error
}
