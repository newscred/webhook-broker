package dispatcher

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imyousuf/webhook-broker/config"
)

// Worker represents the worker that executes the job
type Worker struct {
	workerPool               chan chan Job
	jobChannel               chan Job
	quit                     chan bool
	consumerConnectionConfig config.ConsumerConnectionConfig
	working                  bool
}

// NewWorker creates a Worker
func NewWorker(workerPool chan chan Job, consumerConfig config.ConsumerConnectionConfig) Worker {
	return Worker{
		workerPool:               workerPool,
		jobChannel:               make(chan Job, 1),
		quit:                     make(chan bool, 1),
		working:                  false,
		consumerConnectionConfig: consumerConfig}
}

// Start method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (w *Worker) Start() {
	var client = &http.Client{Timeout: time.Second * 10}
	go func() {
		w.working = true
		for {
			// register the current worker into the worker queue.
			w.workerPool <- w.jobChannel

			select {
			case job := <-w.jobChannel:
				// we have received a work request.
				req, reqErr := http.NewRequest("POST", "http://localhost:58080/consumer", strings.NewReader(job.Data.Message.Payload))
				if reqErr != nil {
					fmt.Println(reqErr)
					return
				}
				req.Header.Set("Content-Type", "text/plain")
				req.Header.Set("X-Broker-Message-Priority", strconv.Itoa(int(job.Priority)))
				_, err := client.Do(req)
				if err != nil {
					fmt.Println(err)
				}
				req.Body.Close()

			case <-w.quit:
				// we have received a signal to stop
				w.working = false
				return
			}
		}
	}()
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