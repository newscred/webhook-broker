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
	QueuedJobCount           prometheus.Gauge
	DeadJobCount             *prometheus.GaugeVec
	ConsumerQueuedJobCount   *prometheus.GaugeVec
	ConsumerInflightJobCount *prometheus.GaugeVec
	// DispatchPaused is 1 per paused consumer (channel, consumer) and for the global pause ("*", "*")
	DispatchPaused *prometheus.GaugeVec
	// ParkedJobCount is the number of parked jobs per consumer, refreshed by the drain lock holder
	ParkedJobCount *prometheus.GaugeVec
	// ParkedJobsTotal counts parked jobs by reason: dispatch, worker, retry, handoff
	ParkedJobsTotal *prometheus.CounterVec
	// DrainedJobsTotal counts jobs moved from parking back into the job queue, per consumer
	DrainedJobsTotal *prometheus.CounterVec
	// HandoffRequestsTotal counts hand-off outcomes: accepted, busy, error, local
	HandoffRequestsTotal *prometheus.CounterVec
	// HandoffFailuresTotal counts hand-off batches for which every attempt failed
	HandoffFailuresTotal prometheus.Counter
}

const (
	// ParkReasonDispatch is the parked_jobs_total reason for jobs parked when the message was dispatched
	ParkReasonDispatch = "dispatch"
	// ParkReasonWorker is the parked_jobs_total reason for jobs re-parked by a worker after the DB hand-out check refused
	ParkReasonWorker = "worker"
	// ParkReasonRetry is the parked_jobs_total reason for jobs re-parked when (re)queued while paused
	ParkReasonRetry = "retry"
	// ParkReasonHandoff is the parked_jobs_total reason for handed-off jobs re-parked because the consumer was paused again
	ParkReasonHandoff = "handoff"
)

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
	container.ConsumerQueuedJobCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "consumer_queued_job_count",
		Help: "Jobs waiting in this instance's dispatcher, per consumer",
	}, []string{"channel", "consumer"})
	container.ConsumerInflightJobCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "consumer_inflight_job_count",
		Help: "Deliveries this instance is running, per consumer",
	}, []string{"channel", "consumer"})
	container.DispatchPaused = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dispatch_paused",
		Help: "1 for every paused consumer; the global pause uses channel and consumer \"*\"",
	}, []string{"channel", "consumer"})
	container.ParkedJobCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "parked_job_count",
		Help: "Number of parked jobs per consumer",
	}, []string{"channel", "consumer"})
	container.ParkedJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "parked_jobs_total",
		Help: "Jobs parked because their consumer was paused, by reason",
	}, []string{"reason"})
	container.DrainedJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "drained_jobs_total",
		Help: "Jobs moved from parking back to the job queue, per consumer",
	}, []string{"consumer"})
	container.HandoffRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "handoff_requests_total",
		Help: "Drained job hand-off requests by outcome",
	}, []string{"outcome"})
	container.HandoffFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "handoff_failures_total",
		Help: "Hand-off batches for which every attempt failed",
	})
	return container
}

func NewPrometheusHandler() http.Handler {
	return promhttp.Handler()
}
