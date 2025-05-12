package scheduler

import (
	"sync/atomic"
	"time"
)

// MetricsContainer encapsulates scheduler-related metrics
type MetricsContainer struct {
	// ScheduledMessages is the number of messages created with scheduling
	ScheduledMessages uint64
	// DispatchedScheduledMessages is the number of scheduled messages dispatched
	DispatchedScheduledMessages uint64
	// SchedulingErrors is the number of errors encountered during scheduling
	SchedulingErrors uint64
	// LatestDispatchLag is the duration between scheduled and actual dispatch time
	LatestDispatchLag time.Duration
}

// NewMetricsContainer creates a new metrics container
func NewMetricsContainer() *MetricsContainer {
	return &MetricsContainer{}
}

// IncreaseScheduledMessageCount increases the scheduled message count
func (m *MetricsContainer) IncreaseScheduledMessageCount() uint64 {
	return atomic.AddUint64(&m.ScheduledMessages, 1)
}

// IncreaseDispatchedScheduledMessageCount increases the dispatched scheduled message count
func (m *MetricsContainer) IncreaseDispatchedScheduledMessageCount() uint64 {
	return atomic.AddUint64(&m.DispatchedScheduledMessages, 1)
}

// IncreaseSchedulingErrorCount increases the scheduling error count
func (m *MetricsContainer) IncreaseSchedulingErrorCount() uint64 {
	return atomic.AddUint64(&m.SchedulingErrors, 1)
}

// SetLatestDispatchLag sets the latest dispatch lag time
func (m *MetricsContainer) SetLatestDispatchLag(lag time.Duration) {
	atomic.StoreInt64((*int64)(&m.LatestDispatchLag), int64(lag))
}

// GetLatestDispatchLag gets the latest dispatch lag time
func (m *MetricsContainer) GetLatestDispatchLag() time.Duration {
	return time.Duration(atomic.LoadInt64((*int64)(&m.LatestDispatchLag)))
}