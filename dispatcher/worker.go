package dispatcher

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage"
)

const (
	headerContentType    = "Content-Type"
	headerBrokerPriority = "X-Broker-Message-Priority"
	headerConsumerToken  = "X-Broker-Consumer-Token"
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
	// we have received a work request.
	log.Debug().Msg("processing job in worker " + job.Data.ID.String())
	// Put to Inflight
	err := w.djRepo.MarkJobInflight(job.Data)
	if err != nil {
		log.Error().Err(err).Msg("err - could not put job in flight" + job.Data.ID.String())
		return
	}
	// Attempt to deliver
	err = w.executeJob(job)
	// If err == nil, then delivered, else if at max try dead else queued with retry attempt increased
	if err == nil {
		log.Debug().Msg("delivered job " + job.Data.ID.String())
		w.djRepo.MarkJobDelivered(job.Data)
	} else if job.Data.RetryAttemptCount >= uint(w.brokerConfig.GetMaxRetry()) {
		w.djRepo.MarkJobDead(job.Data)
	} else {
		log.Debug().Msg("schedule for retry job " + job.Data.ID.String())
		w.djRepo.MarkJobRetry(job.Data, w.earliestDelta(job.Data.RetryAttemptCount+1))
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

func (w *Worker) executeJob(job *Job) (err error) {
	// Do not let the worker crash due to any panic
	defer func() {
		if r := recover(); r != nil {
			log.Error().Msg(fmt.Sprint("error - panic in executing job -", job.Data.ID, r))
		}
	}()
	var req *http.Request
	req, err = http.NewRequest(http.MethodPost, job.Data.Listener.CallbackURL, strings.NewReader(job.Data.Message.Payload))
	if err == nil {
		defer req.Body.Close()
		req.Header.Set(headerContentType, job.Data.Message.ContentType)
		req.Header.Set(headerBrokerPriority, strconv.Itoa(int(job.Priority)))
		req.Header.Set(headerConsumerToken, job.Data.Listener.Token)
		var resp *http.Response
		resp, err = w.httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			code := resp.StatusCode
			if code < 200 || code > 299 {
				errBody, rErr := ioutil.ReadAll(resp.Body)
				var errString string
				if rErr == nil {
					errString = string(errBody)
				}
				log.Error().Msg(fmt.Sprint("error - consumer connection error ", resp.Status, " ", errString, " ", job.Data.ID))
				err = errConsumer
			}
		}
	}
	if err != nil {
		log.Error().Err(err).Msg("error - worker failed to deliver" + job.Data.ID.String())
	}
	return err
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
