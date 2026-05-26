package gateway

import (
	"sync"
	"sync/atomic"
	"time"
)

var (
	totalTasksEnqueued   int64
	totalTasksCompleted  int64
	totalEventsProcessed int64
	metricErrors         int64
	activeTasks          int64 // current number of running tasks
	queuedTasks          int64 // current number of waiting tasks

	// Connection metrics
	connectionStatus     int64 // 0=disconnected, 1=connected
	disconnectionCount   int64
	lastConnectionTime   time.Time
	lastConnectionTimeMu sync.Mutex // protects lastConnectionTime
)

// MetricsSnapshot returns a point-in-time copy of all runtime metrics.
func MetricsSnapshot() Metrics {
	lastConnectionTimeMu.Lock()
	lct := lastConnectionTime
	lastConnectionTimeMu.Unlock()

	return Metrics{
		TotalTasksEnqueued:   atomic.LoadInt64(&totalTasksEnqueued),
		TotalTasksCompleted:  atomic.LoadInt64(&totalTasksCompleted),
		TotalEventsProcessed: atomic.LoadInt64(&totalEventsProcessed),
		Errors:               atomic.LoadInt64(&metricErrors),
		ActiveTasks:          atomic.LoadInt64(&activeTasks),
		QueuedTasks:          atomic.LoadInt64(&queuedTasks),
		ConnectionStatus:     atomic.LoadInt64(&connectionStatus),
		DisconnectionCount:   atomic.LoadInt64(&disconnectionCount),
		LastConnectionTime:   lct,
	}
}

// Metrics holds a snapshot of all runtime counters.
type Metrics struct {
	TotalTasksEnqueued   int64
	TotalTasksCompleted  int64
	TotalEventsProcessed int64
	Errors               int64
	ActiveTasks          int64 // current running tasks
	QueuedTasks          int64 // current waiting tasks

	// Connection metrics
	ConnectionStatus   int64     // 0=disconnected, 1=connected
	DisconnectionCount int64     // number of disconnections
	LastConnectionTime time.Time // last time connection was established
}

// SetActiveTasks updates the active task counter.
func SetActiveTasks(count int) { atomic.StoreInt64(&activeTasks, int64(count)) }

// SetQueuedTasks updates the queued task counter.
func SetQueuedTasks(count int) { atomic.StoreInt64(&queuedTasks, int64(count)) }

// SetConnectionStatus updates the connection status metric atomically.
// When connected=true, sets status to 1 and records the time.
// When connected=false, sets status to 0 and increments disconnection count.
func SetConnectionStatus(connected bool) {
	if connected {
		atomic.StoreInt64(&connectionStatus, 1)
		lastConnectionTimeMu.Lock()
		lastConnectionTime = time.Now()
		lastConnectionTimeMu.Unlock()
	} else {
		atomic.StoreInt64(&connectionStatus, 0)
		atomic.AddInt64(&disconnectionCount, 1)
	}
}

// ResetMetrics zeroes all metric counters (useful for testing).
func ResetMetrics() {
	atomic.StoreInt64(&totalTasksEnqueued, 0)
	atomic.StoreInt64(&totalTasksCompleted, 0)
	atomic.StoreInt64(&totalEventsProcessed, 0)
	atomic.StoreInt64(&metricErrors, 0)
	atomic.StoreInt64(&activeTasks, 0)
	atomic.StoreInt64(&queuedTasks, 0)
	atomic.StoreInt64(&connectionStatus, 0)
	atomic.StoreInt64(&disconnectionCount, 0)
	lastConnectionTimeMu.Lock()
	lastConnectionTime = time.Time{}
	lastConnectionTimeMu.Unlock()
}
