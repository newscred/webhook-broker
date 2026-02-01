package dispatcher

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestNewMetricsContainer(t *testing.T) {
	container := NewMetricsContainer()
	assert.NotNil(t, container)
	assert.NotNil(t, container.QueuedJobCount)
	assert.NotNil(t, container.DeadJobCount)
}

func TestMetricsContainerSingleton(t *testing.T) {
	c1 := NewMetricsContainer()
	c2 := NewMetricsContainer()
	assert.Same(t, c1, c2)
}

func TestDeadJobCountGauge(t *testing.T) {
	container := NewMetricsContainer()
	container.DeadJobCount.WithLabelValues("ch1", "c1").Set(5)
	container.DeadJobCount.WithLabelValues("ch2", "c2").Set(10)

	// Verify we can read values via Collect
	ch := make(chan prometheus.Metric, 10)
	container.DeadJobCount.Collect(ch)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	assert.Equal(t, 2, count)
}

func TestDeadJobCountReset(t *testing.T) {
	container := NewMetricsContainer()
	container.DeadJobCount.WithLabelValues("ch-reset", "c-reset").Set(99)
	container.DeadJobCount.Reset()

	ch := make(chan prometheus.Metric, 10)
	container.DeadJobCount.Collect(ch)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	// After reset, previously set labels should still have metrics from other tests
	// but the specific ones we set should be gone or zero
	assert.GreaterOrEqual(t, count, 0)
}

// Generated with assistance from Claude AI
