package tests

import (
	"sync"

	gw "github.com/agentfactory/gateway/gateway"
	statemgr "github.com/agentfactory/gateway/state"

	"github.com/slack-go/slack"
)

// MockSlackClient implements the gateway.SlackClient interface.
// It records all PostMessage and UpdateMessage calls for later inspection.
type MockSlackClient struct {
	mu             sync.Mutex
	Updates        []MockUpdate
	Posts          []MockPost
	UpdateErr      error
	PostErr        error
}

// MockUpdate records a single UpdateMessage call.
type MockUpdate struct {
	ChannelID string
	Timestamp string
	Options   []slack.MsgOption
}

// MockPost records a single PostMessage call.
type MockPost struct {
	ChannelID string
	Options   []slack.MsgOption
}

// UpdateMessage records the call and returns configured error (if any).
func (m *MockSlackClient) UpdateMessage(channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Updates = append(m.Updates, MockUpdate{
		ChannelID: channelID,
		Timestamp: timestamp,
		Options:   options,
	})
	return channelID, timestamp, "", m.UpdateErr
}

// PostMessage records the call and returns configured error (if any).
func (m *MockSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Posts = append(m.Posts, MockPost{
		ChannelID: channelID,
		Options:   options,
	})
	return channelID, "", m.PostErr
}

// MockStatusChecker implements the gateway.StatusChecker interface.
// It returns configurable status and error values per task ID.
type MockStatusChecker struct {
	Statuses map[string]string // taskID -> status
	Errors   map[string]error  // taskID -> error
}

// CheckStatus returns the configured status/error for the given task ID.
func (m *MockStatusChecker) CheckStatus(taskID string) (string, error) {
	if m.Errors != nil {
		if err, ok := m.Errors[taskID]; ok {
			return "", err
		}
	}
	if m.Statuses != nil {
		if status, ok := m.Statuses[taskID]; ok {
			return status, nil
		}
	}
	return "unknown", nil
}

// Verify interface compliance at compile time.
var _ gw.SlackClient = (*MockSlackClient)(nil)
var _ gw.StatusChecker = (*MockStatusChecker)(nil)

// NewTestStateManager creates a StateManager backed by a temporary file
// for testing purposes. The file is stored in t.TempDir().
func NewTestStateManager(t testingT) statemgr.StateManager {
	t.Helper()
	sm, err := statemgr.NewJSONStateManager(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}
	return sm
}

// testingT is a minimal interface matching testing.T for the helper.
type testingT interface {
	Helper()
	Fatalf(format string, args ...interface{})
	TempDir() string
}
