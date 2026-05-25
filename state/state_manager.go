package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// TaskRecord represents the persisted state of a single task.
type TaskRecord struct {
	TaskID    string    `json:"task_id"`
	ChannelID string    `json:"channel_id"`
	SlackTS   string    `json:"slack_ts"`
	UserID    string    `json:"user_id"`
	Prompt    string    `json:"prompt"`       // Original user prompt (for retry)
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StateManager persists and manages task gateway state.
type StateManager struct {
	mu       sync.RWMutex
	records  map[string]*TaskRecord
	filePath string
}

// NewStateManager creates a StateManager that persists to the given file path.
// Returns an empty manager if the file doesn't exist. Returns an error if the
// file exists but contains corrupted data.
func NewStateManager(filePath string) (*StateManager, error) {
	sm := &StateManager{
		records:  make(map[string]*TaskRecord),
		filePath: filePath,
	}
	if err := sm.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load state: %w", err)
	}
	return sm, nil
}

// Set creates or updates a task record. If UpdatedAt is zero, it is set to now.
// For existing records, non-zero fields in the incoming record overwrite stored
// values; zero fields preserve the existing stored value.
func (sm *StateManager) Set(rec TaskRecord) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if existing, ok := sm.records[rec.TaskID]; ok {
		// Merge: only overwrite non-zero fields.
		if rec.ChannelID != "" {
			existing.ChannelID = rec.ChannelID
		}
		if rec.SlackTS != "" {
			existing.SlackTS = rec.SlackTS
		}
		if rec.UserID != "" {
			existing.UserID = rec.UserID
		}
		if rec.Prompt != "" {
			existing.Prompt = rec.Prompt
		}
		if rec.Status != "" {
			existing.Status = rec.Status
		}
		existing.UpdatedAt = time.Now().UTC()
	} else {
		if rec.UpdatedAt.IsZero() {
			rec.UpdatedAt = time.Now().UTC()
		}
		cpy := rec
		sm.records[rec.TaskID] = &cpy
	}

	return sm.saveLocked()
}

// Get returns a copy of a task's record, or nil/false if not found.
func (sm *StateManager) Get(taskID string) (*TaskRecord, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	rec, ok := sm.records[taskID]
	if !ok {
		return nil, false
	}

	// Return a copy to avoid race conditions.
	cpy := *rec
	return &cpy, true
}

// ListActive returns all records with status "running".
func (sm *StateManager) ListActive() []*TaskRecord {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []*TaskRecord
	for _, rec := range sm.records {
		if rec.Status == "running" {
			cpy := *rec
			result = append(result, &cpy)
		}
	}
	return result
}

// load reads state from disk. The file is stored as a flat map[string]TaskRecord.
func (sm *StateManager) load() error {
	data, err := os.ReadFile(sm.filePath)
	if err != nil {
		return err
	}

	var records map[string]TaskRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("unmarshal state: %w", err)
	}

	sm.records = make(map[string]*TaskRecord, len(records))
	for k, v := range records {
		cpy := v
		sm.records[k] = &cpy
	}
	return nil
}

// saveLocked writes state to disk atomically. Caller must hold sm.mu (write lock).
// Uses write-to-tmp + rename to prevent corruption on crash during write.
func (sm *StateManager) saveLocked() error {
	// Serialize as a flat map for simplicity.
	flat := make(map[string]TaskRecord, len(sm.records))
	for k, v := range sm.records {
		flat[k] = *v
	}

	data, err := json.MarshalIndent(flat, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	// Atomic write: write to temp file, then rename.
	tmpPath := sm.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	return os.Rename(tmpPath, sm.filePath)
}

// HasActiveTask returns true if the given channel has a running or paused task.
// Used to prevent concurrent task execution in the same channel.
func (sm *StateManager) HasActiveTask(channelID string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, rec := range sm.records {
		if rec.ChannelID == channelID && (rec.Status == "running" || rec.Status == "paused") {
			return true
		}
	}
	return false
}

// GetByChannel returns the most recent task record for a given channel,
// regardless of status. Useful for retry operations.
func (sm *StateManager) GetByChannel(channelID string) (*TaskRecord, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var latest *TaskRecord
	for _, rec := range sm.records {
		if rec.ChannelID == channelID {
			if latest == nil || rec.UpdatedAt.After(latest.UpdatedAt) {
				cpy := *rec
				latest = &cpy
			}
		}
	}
	return latest, latest != nil
}
