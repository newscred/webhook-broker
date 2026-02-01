package dispatcher

import (
	"net/http"
	"sync"

	"github.com/google/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	MetricsInjector = wire.NewSet(NewMetricsContainer, NewPrometheusHandler)
	sharedContainer *MetricsContainer
	once            sync.Once
)

type MetricsContainer struct {
	QueuedJobCount prometheus.Gauge
	DeadJobCount   *prometheus.GaugeVec
}

func NewMetricsContainer() *MetricsContainer {
	once.Do(func() {
		sharedContainer = newMetricsContainer()
	})
	return sharedContainer
}

func newMetricsContainer() *MetricsContainer {
	container := &MetricsContainer{}
	container.QueuedJobCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "queued_job_count",
		Help: "The current number of jobs in the queue",
	})
	container.DeadJobCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dead_job_count",
		Help: "Number of dead jobs per consumer",
	}, []string{"channel", "consumer"})
	return container
}

func NewPrometheusHandler() http.Handler {
	return promhttp.Handler()
}
