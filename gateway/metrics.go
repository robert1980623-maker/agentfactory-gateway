package gateway

import (
	"sync/atomic"
)

var (
	totalTasksEnqueued   int64
	totalTasksCompleted  int64
	totalEventsProcessed int64
	metricErrors         int64
)

// MetricsSnapshot returns a point-in-time copy of all runtime metrics.
func MetricsSnapshot() Metrics {
	return Metrics{
		TotalTasksEnqueued:   atomic.LoadInt64(&totalTasksEnqueued),
		TotalTasksCompleted:  atomic.LoadInt64(&totalTasksCompleted),
		TotalEventsProcessed: atomic.LoadInt64(&totalEventsProcessed),
		Errors:               atomic.LoadInt64(&metricErrors),
	}
}

// Metrics holds a snapshot of all runtime counters.
type Metrics struct {
	TotalTasksEnqueued   int64
	TotalTasksCompleted  int64
	TotalEventsProcessed int64
	Errors               int64
}

// ResetMetrics zeroes all metric counters (useful for testing).
func ResetMetrics() {
	atomic.StoreInt64(&totalTasksEnqueued, 0)
	atomic.StoreInt64(&totalTasksCompleted, 0)
	atomic.StoreInt64(&totalEventsProcessed, 0)
	atomic.StoreInt64(&metricErrors, 0)
}
