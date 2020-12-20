package dispatcher

import (
	"context"
	"log"
	"time"

	"github.com/imyousuf/webhook-broker/config"
	"github.com/imyousuf/webhook-broker/storage"
	"github.com/imyousuf/webhook-broker/storage/data"
)

const (
	panicString = "parameters null"
)

// MessageDispatcher is the contract for dispatching message
type MessageDispatcher interface {
	Dispatch(message *data.Message)
	Stop()
}

// MessageDispatcherImpl is responsible for dispatching delivery jobs from acknowledged message
type MessageDispatcherImpl struct {
	consumerRepo     storage.ConsumerRepository
	djRepo           storage.DeliveryJobRepository
	workerPool       chan chan *Job
	workers          []*Worker
	jobQueue         chan *Job
	jobPriorityQueue *PriorityQueue
	stopTimeout      time.Duration
	dispatcherStop   chan bool
}

// Dispatch is responsible for dispatching delivery jobs for the message
func (msgDispatcher *MessageDispatcherImpl) Dispatch(message *data.Message) {
	if message == nil || !message.IsInValidState() {
		return
	}
	channelID := message.BroadcastedTo.ChannelID
	consumers := make([]*data.Consumer, 0)
	page := data.NewPagination(nil, nil)
	more := true
	var err error
	for more {
		var consumersPage []*data.Consumer
		consumersPage, page, err = msgDispatcher.consumerRepo.GetList(channelID, page)
		more = page.Next != nil && err == nil
		page.Previous = nil
		consumers = append(consumers, consumersPage...)
	}
	jobs := make([]*data.DeliveryJob, len(consumers))
	for index, consumer := range consumers {
		if err == nil {
			jobs[index], err = data.NewDeliveryJob(message, consumer)
		}
	}
	if err == nil {
		err = msgDispatcher.djRepo.DispatchMessage(message, jobs...)
	}
	if err == nil {
		for _, job := range jobs {
			msgDispatcher.jobQueue <- NewJob(job)
		}
	}
	if err != nil {
		log.Println("error dispatching -", err)
	}
}

// StartDispatcher starts consuming jobs and should be called as a coroutine.
func (msgDispatcher *MessageDispatcherImpl) StartDispatcher() {
	go func() {
		for {
			select {
			case job := <-msgDispatcher.jobQueue:
				msgDispatcher.dispatchJob(job)
			case <-msgDispatcher.dispatcherStop:
				return
			}
		}
	}()
}

// Stop stops the workers of the dispatcher
func (msgDispatcher *MessageDispatcherImpl) Stop() {
	msgDispatcher.dispatcherStop <- true
	timeoutContext, cancelFunc := context.WithTimeout(context.Background(), msgDispatcher.stopTimeout)
	defer cancelFunc()
	select {
	case <-timeoutContext.Done():
		log.Println("warn - dispatcher stop timedout")
		return
	default:
		log.Println("stopping workers", len(msgDispatcher.workers))
		anyRunning := true
		for i := 0; i < len(msgDispatcher.workers); i++ {
			msgDispatcher.workers[i].Stop()
		}
		for anyRunning {
			localRun := false
			for i := 0; i < len(msgDispatcher.workers); i++ {
				localRun = localRun || msgDispatcher.workers[i].IsWorking()
			}
			anyRunning = localRun
		}
	}
}

var asyncDequeueToWorker = func(msgDispatcher *MessageDispatcherImpl) {
	// try to obtain a worker job channel that is available.
	// this will block until a worker is idle
	jobChannel := <-msgDispatcher.workerPool

	// dispatch the job to the worker job channel
	jobChannel <- msgDispatcher.jobPriorityQueue.Dequeue()
}

func (msgDispatcher *MessageDispatcherImpl) dispatchJob(job *Job) {
	msgDispatcher.jobPriorityQueue.Enqueue(job)
	// a job request has been received
	go asyncDequeueToWorker(msgDispatcher)
}

// NewMessageDispatcher retrieves new instance of MessageDispatcher
func NewMessageDispatcher(djRepo storage.DeliveryJobRepository, consumerRepo storage.ConsumerRepository, brokerConfig config.BrokerConfig, consumerConfig config.ConsumerConnectionConfig) MessageDispatcher {
	if djRepo == nil || consumerRepo == nil || brokerConfig == nil || consumerConfig == nil {
		panic(panicString)
	}
	dispatcherImpl := &MessageDispatcherImpl{djRepo: djRepo, consumerRepo: consumerRepo, dispatcherStop: make(chan bool),
		workerPool: make(chan chan *Job, brokerConfig.GetMaxWorkers()), jobPriorityQueue: NewJobPriorityQueue(),
		jobQueue: make(chan *Job, brokerConfig.GetMaxMessageQueueSize())}
	workers := make([]*Worker, brokerConfig.GetMaxWorkers())
	for i := 0; i < len(workers); i++ {
		worker := NewWorker(dispatcherImpl.workerPool, consumerConfig, brokerConfig, djRepo)
		worker.Start()
		workers[i] = &worker
	}
	dispatcherImpl.workers = workers
	dispatcherImpl.stopTimeout = consumerConfig.GetConnectionTimeout() + 250*time.Millisecond
	dispatcherImpl.StartDispatcher()
	return dispatcherImpl
}
