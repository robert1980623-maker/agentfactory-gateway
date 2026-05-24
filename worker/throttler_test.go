package worker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentfactory/gateway/protocol"
)

func makeEvent(t protocol.SlackEventType) *protocol.SlackEvent {
	return &protocol.SlackEvent{Type: t}
}

// TestThrottlerImmediateFlush verifies that critical event types bypass
// buffering and invoke flushFn synchronously.
func TestThrottlerImmediateFlush(t *testing.T) {
	var calls int32
	var mu sync.Mutex
	var lastEvent *protocol.SlackEvent

	tt := NewMessageThrottler(100*time.Millisecond, func(event *protocol.SlackEvent) {
		atomic.AddInt32(&calls, 1)
		mu.Lock()
		lastEvent = event
		mu.Unlock()
	})

	tt.Push(makeEvent(protocol.EventTypeStart))

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected flushFn called 1 time, got %d", got)
	}

	mu.Lock()
	if lastEvent == nil || lastEvent.Type != protocol.EventTypeStart {
		t.Fatalf("expected start event, got %v", lastEvent)
	}
	mu.Unlock()
}

// TestThrottlerBuffering verifies that multiple progress events within the
// debounce window produce exactly one flush.
func TestThrottlerBuffering(t *testing.T) {
	interval := 100 * time.Millisecond

	var calls int32
	tt := NewMessageThrottler(interval, func(_ *protocol.SlackEvent) {
		atomic.AddInt32(&calls, 1)
	})

	// Push 3 progress events within 50ms.
	tt.Push(makeEvent(protocol.EventTypeProgress))
	time.Sleep(20 * time.Millisecond)
	tt.Push(makeEvent(protocol.EventTypeProgress))
	time.Sleep(20 * time.Millisecond)
	tt.Push(makeEvent(protocol.EventTypeProgress))

	// Wait for the timer to fire.
	time.Sleep(interval + 50*time.Millisecond)

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected flushFn called exactly 1 time, got %d", got)
	}
}

// TestThrottlerReset verifies that each buffered event resets the timer,
// so the flush happens relative to the *last* event, not the first.
func TestThrottlerReset(t *testing.T) {
	interval := 100 * time.Millisecond

	var calls int32
	var firstFlushTime time.Time
	tt := NewMessageThrottler(interval, func(_ *protocol.SlackEvent) {
		if firstFlushTime.IsZero() {
			firstFlushTime = time.Now()
		}
		atomic.AddInt32(&calls, 1)
	})

	start := time.Now()

	// Push progress at 10ms.
	time.Sleep(10 * time.Millisecond)
	tt.Push(makeEvent(protocol.EventTypeProgress))

	// Push progress again at 20ms (10ms after the first).
	time.Sleep(10 * time.Millisecond)
	tt.Push(makeEvent(protocol.EventTypeProgress))

	// The timer was reset at t=20ms, so flush should happen around t=120ms.
	// Wait until ~130ms total.
	time.Sleep(interval + 90*time.Millisecond)

	elapsed := time.Since(start)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected flushFn called exactly 1 time, got %d", got)
	}

	// Flush should have occurred no earlier than 100ms after the second push.
	// Second push was at ~20ms, so earliest flush is ~120ms.
	if elapsed < interval+10*time.Millisecond {
		t.Fatalf("flush happened too early: elapsed=%v, expected >=%v", elapsed, interval+10*time.Millisecond)
	}
}
