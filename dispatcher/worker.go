package dispatcher

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/xid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage"
)

const (
	headerContentType     = "Content-Type"
	headerBrokerPriority  = "X-Broker-Message-Priority"
	headerChannelID       = "X-Broker-Channel-ID"
	headerConsumerToken   = "X-Broker-Consumer-Token"
	headerConsumerID      = "X-Broker-Consumer-ID"
	headerMessageID       = "X-Broker-Message-ID"
	headerMetadataHeaders = "X-Broker-Metadata-Headers"
	headerRequestID       = "X-Request-ID"
	headerUserAgent       = "User-Agent"
	requestIDLogFieldKey  = "requestId"
	jobIDLogFieldKey      = "jobId"
)

var (
	errConsumer = errors.New("error - client status not 2xx")
)

// Worker represents the worker that executes the job
type Worker struct {
	workerPool               chan chan *Job
	jobChannel               chan *Job
	quit                     chan bool
	consumerConnectionConfig config.ConsumerConnectionConfig
	brokerConfig             config.BrokerConfig
	working                  bool
	djRepo                   storage.DeliveryJobRepository
	httpClient               *http.Client
}

// NewWorker creates a Worker
func NewWorker(workerPool chan chan *Job, consumerConfig config.ConsumerConnectionConfig, brokerConfig config.BrokerConfig, deliveryJobRepo storage.DeliveryJobRepository) Worker {
	return Worker{
		workerPool:               workerPool,
		jobChannel:               make(chan *Job, 1),
		quit:                     make(chan bool, 1),
		working:                  false,
		consumerConnectionConfig: consumerConfig,
		brokerConfig:             brokerConfig,
		djRepo:                   deliveryJobRepo,
		httpClient:               createHTTPClient(consumerConfig)}
}

func createHTTPClient(consumerConfig config.ConsumerConnectionConfig) *http.Client {
	return &http.Client{Timeout: consumerConfig.GetConnectionTimeout()}
}

var deliverJob = func(w *Worker, job *Job) {
	reqID := xid.New().String()
	logger := log.With().Str(requestIDLogFieldKey, reqID).Str(jobIDLogFieldKey, job.Data.ID.String()).Logger()
	// we have received a work request.
	logger.Debug().Msg("processing job in worker ")
	// Put to Inflight
	err := w.djRepo.MarkJobInflight(job.Data)
	if err != nil {
		logger.Error().Err(err).Msg("err - could not put job in flight")
		return
	}
	// Attempt to deliver
	err = w.executeJob(reqID, logger, job)
	// If err == nil, then delivered, else if at max try dead else queued with retry attempt increased
	if err == nil {
		logger.Debug().Msg("delivered job")
		err = w.djRepo.MarkJobDelivered(job.Data)
	} else if job.Data.RetryAttemptCount >= uint(w.brokerConfig.GetMaxRetry()) {
		logger.Debug().Err(err).Msg("job marked dead")
		err = w.djRepo.MarkJobDead(job.Data)
	} else {
		logger.Debug().Err(err).Msg("schedule for retry job ")
		err = w.djRepo.MarkJobRetry(job.Data, w.earliestDelta(job.Data.RetryAttemptCount+1))
	}
	if err != nil {
		logger.Error().Err(err).Msg("Could not update job status")
	}
}

// Start method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (w *Worker) Start() {
	go func() {
		w.working = true
		for {
			// register the current worker into the worker queue.
			w.workerPool <- w.jobChannel

			select {
			case job := <-w.jobChannel:
				deliverJob(w, job)
			case <-w.quit:
				// we have received a signal to stop
				w.working = false
				return
			}
		}
	}()
}

func (w *Worker) earliestDelta(retryAttempt uint) time.Duration {
	return computeEarliestDelta(retryAttempt, w.brokerConfig)
}

var callConsumer = func(worker *Worker, requestID string, logger zerolog.Logger, job *Job) (err error) {
	var req *http.Request
	req, err = http.NewRequest(http.MethodPost, job.Data.Listener.CallbackURL, strings.NewReader(job.Data.Message.Payload))
	if err == nil {
		defer req.Body.Close()
		message := job.Data.Message
		req.Header.Set(headerContentType, message.ContentType)
		req.Header.Set(headerBrokerPriority, strconv.Itoa(int(job.Priority)))
		req.Header.Set(headerChannelID, job.Data.Message.BroadcastedTo.ChannelID)
		req.Header.Set(headerConsumerID, job.Data.Listener.ConsumerID)
		req.Header.Set(worker.consumerConnectionConfig.GetTokenRequestHeaderName(), job.Data.Listener.Token)
		req.Header.Set(headerUserAgent, worker.consumerConnectionConfig.GetUserAgent())
		req.Header.Set(headerRequestID, requestID)
		req.Header.Set(headerMessageID, message.MessageID)
		metadataHeaderNames := []string{}
		for key, val := range message.Headers {
			req.Header.Set(key, val)
			metadataHeaderNames = append(metadataHeaderNames, key)
		}
		sort.Slice(metadataHeaderNames, func(i, j int) bool {
			return metadataHeaderNames[i] < metadataHeaderNames[j]
		})
		req.Header.Add(headerMetadataHeaders, strings.Join(metadataHeaderNames, ","))

		var resp *http.Response
		httpClient := worker.httpClient
		resp, err = httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			code := resp.StatusCode
			if code < 200 || code > 299 {
				errBody, rErr := ioutil.ReadAll(resp.Body)
				var errString string
				if rErr == nil {
					errString = string(errBody)
				}
				logger.Error().Msg(fmt.Sprint("error - consumer connection error ", resp.Status, " ", errString))
				err = errConsumer
			}
		}
	}
	if err != nil {
		logger.Error().Err(err).Msg("error - worker failed to deliver")
	}
	return err
}

func (w *Worker) executeJob(requestID string, logger zerolog.Logger, job *Job) (err error) {
	// Do not let the worker crash due to any panic
	defer func() {
		if r := recover(); r != nil {
			logger.Error().Msg(fmt.Sprint("error - panic in executing job - ", r))
			err = errors.New("panic in executeJob")
		}
	}()
	return callConsumer(w, requestID, logger, job)
}

// IsWorking retrieves whether the work is active
func (w *Worker) IsWorking() bool {
	return w.working
}

// Stop signals the worker to stop listening for work requests.
func (w *Worker) Stop() {
	go func() {
		w.quit <- true
	}()
}
