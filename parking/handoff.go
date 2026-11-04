package parking

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage/data"
)

const (
	outcomeAccepted = "accepted"
	outcomeBusy     = "busy"
	outcomeError    = "error"
	outcomeLocal    = "local"
)

var (
	// handoffSleep is the backoff sleep between hand-off attempts; replaceable in tests
	handoffSleep = func(attempt int) {
		base := time.Duration(attempt) * 100 * time.Millisecond
		time.Sleep(base + time.Duration(rand.Int63n(int64(base)+1)))
	}
	// handoffNow returns the time used for request signatures; replaceable in tests
	handoffNow = time.Now
)

// HandoffSenderConfiguration holds what the sender needs
type HandoffSenderConfiguration struct {
	Dispatcher    dispatcher.MessageDispatcher
	Metrics       *dispatcher.MetricsContainer
	HTTPEnabled   bool
	Endpoint      string
	AdminToken    string
	HMACSecret    []byte
	Timeout       time.Duration
	MaxAttempts   int
	Concurrency   int
	LocalFallback bool
}

// HandoffSender asynchronously hands drained jobs to the cluster: it POSTs their ids to the load-balanced internal
// enqueue endpoint, retrying (each retry goes back through the load balancer) and optionally falling back to the
// local queue. Jobs are already durable as QUEUED; a lost hand-off is picked up by the retry worker after the lease.
type HandoffSender struct {
	cfg     HandoffSenderConfiguration
	client  *http.Client
	pending chan []*data.DeliveryJob
	mutex   sync.Mutex
	closed  bool
	wg      sync.WaitGroup
}

// NewHandoffSender creates a sender; call Start before Submit
func NewHandoffSender(cfg HandoffSenderConfiguration) *HandoffSender {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	return &HandoffSender{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}, pending: make(chan []*data.DeliveryJob, cfg.Concurrency*2)}
}

// Start launches the hand-off workers
func (sender *HandoffSender) Start() {
	for i := 0; i < sender.cfg.Concurrency; i++ {
		sender.wg.Add(1)
		go func() {
			defer sender.wg.Done()
			for jobs := range sender.pending {
				sender.send(jobs)
			}
		}()
	}
}

// Stop stops accepting batches and waits for in-flight batches to finish
func (sender *HandoffSender) Stop() {
	sender.mutex.Lock()
	if !sender.closed {
		sender.closed = true
		close(sender.pending)
	}
	sender.mutex.Unlock()
	sender.wg.Wait()
}

// Submit queues a batch for hand-off without blocking; returns false if the sender is saturated or stopped
func (sender *HandoffSender) Submit(jobs []*data.DeliveryJob) bool {
	if len(jobs) == 0 {
		return true
	}
	sender.mutex.Lock()
	defer sender.mutex.Unlock()
	if sender.closed {
		return false
	}
	select {
	case sender.pending <- jobs:
		return true
	default:
		return false
	}
}

// Saturated returns true when no further batch can be submitted without blocking
func (sender *HandoffSender) Saturated() bool {
	return len(sender.pending) >= cap(sender.pending)
}

func (sender *HandoffSender) send(jobs []*data.DeliveryJob) {
	if !sender.cfg.HTTPEnabled {
		sender.enqueueLocally(jobs)
		return
	}
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID.String())
	}
	body, err := json.Marshal(EnqueueRequest{JobIDs: ids})
	if err != nil {
		log.Error().Err(err).Msg("could not encode hand-off request")
		return
	}
	for attempt := 1; attempt <= sender.cfg.MaxAttempts; attempt++ {
		status, postErr := sender.post(body)
		switch {
		case postErr == nil && status == http.StatusAccepted:
			sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeAccepted).Inc()
			return
		case postErr == nil && status == http.StatusServiceUnavailable:
			sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeBusy).Inc()
		default:
			sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeError).Inc()
			log.Warn().Err(postErr).Int("status", status).Int("attempt", attempt).Msg("hand-off attempt failed")
		}
		if attempt < sender.cfg.MaxAttempts {
			handoffSleep(attempt)
		}
	}
	sender.cfg.Metrics.HandoffFailuresTotal.Inc()
	if sender.cfg.LocalFallback {
		log.Warn().Int("jobs", len(jobs)).Msg("all hand-off attempts failed; enqueueing locally")
		sender.enqueueLocally(jobs)
		return
	}
	log.Warn().Int("jobs", len(jobs)).Msg("all hand-off attempts failed; jobs will be retried after the hand-off lease")
}

func (sender *HandoffSender) enqueueLocally(jobs []*data.DeliveryJob) {
	sender.cfg.Dispatcher.EnqueueJobs(jobs)
	sender.cfg.Metrics.HandoffRequestsTotal.WithLabelValues(outcomeLocal).Inc()
}

func (sender *HandoffSender) post(body []byte) (int, error) {
	req, err := http.NewRequest(http.MethodPost, sender.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(AdminTokenHeader, sender.cfg.AdminToken)
	if len(sender.cfg.HMACSecret) > 0 {
		req.Header.Set(SignatureHeader, SignPayload(sender.cfg.HMACSecret, handoffNow().Unix(), body))
	}
	resp, err := sender.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// SignPayload builds the X-Broker-Signature value expected by the gateway HMAC middleware:
// t={timestamp},v1={hex(HMAC-SHA256(secret, "{timestamp}.{body}"))}
func SignPayload(secret []byte, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "." + string(body)))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

// BuildEnqueueEndpoint joins the retrigger base endpoint and the internal enqueue path
func BuildEnqueueEndpoint(retriggerBaseEndpoint string) string {
	return strings.TrimRight(retriggerBaseEndpoint, "/") + InternalEnqueuePath
}

// Generated with assistance from Claude AI
