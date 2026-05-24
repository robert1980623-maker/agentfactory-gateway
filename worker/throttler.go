package worker

import (
	"sync"
	"time"

	"github.com/agentfactory/gateway/protocol"
)

// immediateTypes are event types that bypass buffering and are flushed right away.
var immediateTypes = map[protocol.SlackEventType]bool{
	protocol.EventTypeStart:    true,
	protocol.EventTypeDone:     true,
	protocol.EventTypeError:    true,
	protocol.EventTypeToolCall: true,
}

// MessageThrottler buffers high-frequency events and flushes them at a
// configurable interval, while passing critical events through immediately.
type MessageThrottler struct {
	interval time.Duration
	flushFn  func(event *protocol.SlackEvent)

	mu      sync.Mutex
	buffer  *protocol.SlackEvent
	timer   *time.Timer
	stopped bool
}

// NewMessageThrottler creates a throttler that flushes buffered events at
// the given interval. flushFn is invoked for every flushed event.
func NewMessageThrottler(interval time.Duration, flushFn func(event *protocol.SlackEvent)) *MessageThrottler {
	return &MessageThrottler{
		interval: interval,
		flushFn:  flushFn,
	}
}

// Push submits an event to the throttler.
//
// Immediate event types (start, done, error, tool_call) call flushFn
// synchronously without buffering.
//
// Bufferable event types (progress, dispatch) update the internal buffer
// and reset the flush timer. When the timer fires, the buffered event is
// flushed via flushFn.
func (t *MessageThrottler) Push(event *protocol.SlackEvent) {
	if immediateTypes[event.Type] {
		t.flushFn(event)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stopped {
		return
	}

	t.buffer = event

	if t.timer == nil {
		t.timer = time.AfterFunc(t.interval, t.flush)
	} else {
		t.timer.Reset(t.interval)
	}
}

// Stop prevents further events from being buffered and flushes any
// remaining buffered event. Returns the throttler to a stopped state.
func (t *MessageThrottler) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}

	buf := t.buffer
	t.buffer = nil
	t.stopped = true

	if buf != nil {
		t.flushFn(buf)
	}
}

// Flush immediately sends any buffered event. Useful for final cleanup.
func (t *MessageThrottler) Flush() {
	t.mu.Lock()
	buf := t.buffer
	t.buffer = nil
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()

	if buf != nil {
		t.flushFn(buf)
	}
}

// flush is called by the timer. It reads and clears the buffer under the
// lock, then invokes flushFn outside the lock to prevent deadlocks.
func (t *MessageThrottler) flush() {
	t.mu.Lock()
	buf := t.buffer
	t.buffer = nil
	t.timer = nil
	t.mu.Unlock()

	if buf != nil {
		t.flushFn(buf)
	}
}
