package dispatcher

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

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
	consumerRepo                      storage.ConsumerRepository
	djRepo                            storage.DeliveryJobRepository
	lockRepo                          storage.LockRepository
	msgRepo                           storage.MessageRepository
	workerPool                        chan chan *Job
	workers                           []*Worker
	jobQueue                          chan *Job
	jobPriorityQueue                  *PriorityQueue
	stopTimeout                       time.Duration
	rationalDelay                     time.Duration
	brokerConfig                      config.BrokerConfig
	dispatcherStop                    chan bool
	messageRecoverWorkerStop          chan bool
	jobRecoverStaleInflightWorkerStop chan bool
	jobRecoverRetryWorkerStop         chan bool
	recoveryWorkersEnabled            bool
}

// Dispatch is responsible for dispatching delivery jobs for the message
func (msgDispatcher *MessageDispatcherImpl) Dispatch(message *data.Message) {
	if message == nil || !message.IsInValidState() {
		return
	}
	jobs, err := createJobs(msgDispatcher, message)
	if err == nil {
		err = msgDispatcher.djRepo.DispatchMessage(message, jobs...)
	}
	if err == nil {
		for _, job := range jobs {
			queueJob(msgDispatcher, job)
		}
	}
	if err != nil {
		log.Print("error dispatching -", err)
	}
}

func (msgDispatcher *MessageDispatcherImpl) startMessageDispatcher() {
	for {
		select {
		case job := <-msgDispatcher.jobQueue:
			msgDispatcher.dispatchJob(job)
		case <-msgDispatcher.dispatcherStop:
			return
		}
	}
}

var (
	createJobs = func(msgDispatcher *MessageDispatcherImpl, message *data.Message) ([]*data.DeliveryJob, error) {
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
		return jobs, err
	}

	queueJob = func(msgDispatcher *MessageDispatcherImpl, job *data.DeliveryJob) {
		msgDispatcher.jobQueue <- NewJob(job)
	}

	genericPanicRecoveryFunc = func() {
		if r := recover(); r != nil {
			log.Print("error - had to recover from panic", r)
		}
	}

	attemptMessageDispatch = func(msgDispatcher MessageDispatcher, message *data.Message) (err error) {
		msgDispatcher.Dispatch(message)
		return err
	}

	recoverMessagesNotYetDispatched = func(msgDispatcher *MessageDispatcherImpl) {
		defer genericPanicRecoveryFunc()
		msgDispatcher.lockRepo.TimeoutLocks(msgDispatcher.rationalDelay)
		messages := msgDispatcher.msgRepo.GetMessagesNotDispatchedForCertainPeriod(msgDispatcher.rationalDelay)
		for _, message := range messages {
			err := inLockRun(msgDispatcher.lockRepo, message, func() error {
				return attemptMessageDispatch(msgDispatcher, message)
			})
			if err != nil {
				log.Print("error - could ensure dispatch from recover worker", err, message.MessageID)
			}
		}
	}

	computeEarliestDelta = func(retryAttempt uint, brokerConfig config.BrokerConfig) time.Duration {
		backoffsCount := len(brokerConfig.GetRetryBackoffDelays())
		if retryAttempt < uint(backoffsCount) {
			return brokerConfig.GetRetryBackoffDelays()[int(retryAttempt)-1]
		}
		return time.Duration(int(retryAttempt)-backoffsCount+1) * brokerConfig.GetRetryBackoffDelays()[backoffsCount-1]
	}

	retryQueuedJobs = func(msgDispatcher *MessageDispatcherImpl) {
		defer genericPanicRecoveryFunc()
		jobs := msgDispatcher.djRepo.GetJobsReadyForInflightSince(msgDispatcher.rationalDelay)
		for _, job := range jobs {
			err := inLockRun(msgDispatcher.lockRepo, job, func() error {
				queueJob(msgDispatcher, job)
				return nil
			})
			if err != nil {
				log.Print("error - could not retry job", err, job.ID)
			}
		}
	}

	recoverJobsFromLongInflight = func(msgDispatcher *MessageDispatcherImpl) {
		defer genericPanicRecoveryFunc()
		jobs := msgDispatcher.djRepo.GetJobsInflightSince(msgDispatcher.stopTimeout + msgDispatcher.rationalDelay)
		for _, job := range jobs {
			// Ignore max retry intentionally since we are recovering likely from a process crash during delivery.
			err := inLockRun(msgDispatcher.lockRepo, job, func() error {
				msgDispatcher.djRepo.MarkJobRetry(job, computeEarliestDelta(job.RetryAttemptCount+1, msgDispatcher.brokerConfig))
				return nil
			})
			if err != nil {
				log.Print("error - could not requeue job", err, job.ID)
			}
		}
	}

	inLockRun = func(lockRepo storage.LockRepository, lockable data.Lockable, run func() error) (err error) {
		lock, err := data.NewLock(lockable)
		if err == nil {
			err = lockRepo.TryLock(lock)
		}
		if err == nil {
			defer lockRepo.ReleaseLock(lock)
			err = run()
		}
		if err == storage.ErrAlreadyLocked {
			err = nil
		}
		return err
	}
)

func (msgDispatcher *MessageDispatcherImpl) retryJob() {
	for {
		timer := time.After(msgDispatcher.rationalDelay)
		select {
		case <-msgDispatcher.jobRecoverRetryWorkerStop:
			return
		case <-timer:
			retryQueuedJobs(msgDispatcher)
		}
	}
}

func (msgDispatcher *MessageDispatcherImpl) recoverStaleInflight() {
	for {
		timer := time.After(msgDispatcher.rationalDelay)
		select {
		case <-msgDispatcher.jobRecoverStaleInflightWorkerStop:
			return
		case <-timer:
			recoverJobsFromLongInflight(msgDispatcher)
		}
	}
}

func (msgDispatcher *MessageDispatcherImpl) ensureMessageDispatched() {
	for {
		timer := time.After(msgDispatcher.rationalDelay)
		select {
		case <-msgDispatcher.messageRecoverWorkerStop:
			return
		case <-timer:
			recoverMessagesNotYetDispatched(msgDispatcher)
		}
	}
}

// StartDispatcher starts consuming jobs and should be called as a coroutine.
func (msgDispatcher *MessageDispatcherImpl) StartDispatcher() {
	go msgDispatcher.startMessageDispatcher()
	if msgDispatcher.recoveryWorkersEnabled {
		go msgDispatcher.ensureMessageDispatched()
		go msgDispatcher.recoverStaleInflight()
		go msgDispatcher.retryJob()
	}
}

// Stop stops the workers of the dispatcher
func (msgDispatcher *MessageDispatcherImpl) Stop() {
	timeoutContext, cancelFunc := context.WithTimeout(context.Background(), msgDispatcher.stopTimeout)
	defer cancelFunc()
	select {
	case <-timeoutContext.Done():
		log.Print("warn - dispatcher stop timedout")
		return
	default:
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			msgDispatcher.dispatcherStop <- true
			if msgDispatcher.recoveryWorkersEnabled {
				msgDispatcher.messageRecoverWorkerStop <- true
				msgDispatcher.jobRecoverRetryWorkerStop <- true
				msgDispatcher.jobRecoverStaleInflightWorkerStop <- true
			}
			wg.Done()
		}()
		log.Print("stopping workers", len(msgDispatcher.workers))
		anyRunning := true
		for i := 0; i < len(msgDispatcher.workers); i++ {
			wg.Add(1)
			go func(index int) {
				msgDispatcher.workers[index].Stop()
				wg.Done()
			}(i)
		}
		for anyRunning {
			localRun := false
			for i := 0; i < len(msgDispatcher.workers); i++ {
				localRun = localRun || msgDispatcher.workers[i].IsWorking()
			}
			anyRunning = localRun
		}
		wg.Wait()
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

// Configuration represents the configuration for a dispatcher
type Configuration struct {
	DeliveryJobRepo          storage.DeliveryJobRepository
	ConsumerRepo             storage.ConsumerRepository
	LockRepo                 storage.LockRepository
	MsgRepo                  storage.MessageRepository
	BrokerConfig             config.BrokerConfig
	ConsumerConnectionConfig config.ConsumerConnectionConfig
}

// NewMessageDispatcher retrieves new instance of MessageDispatcher
func NewMessageDispatcher(configuration *Configuration) MessageDispatcher {
	if configuration.DeliveryJobRepo == nil || configuration.ConsumerRepo == nil || configuration.MsgRepo == nil || configuration.LockRepo == nil {
		panic(panicString)
	}
	if configuration.BrokerConfig == nil || configuration.ConsumerConnectionConfig == nil {
		panic(panicString)
	}
	brokerConfig := configuration.BrokerConfig
	djRepo := configuration.DeliveryJobRepo
	consumerRepo := configuration.ConsumerRepo
	consumerConfig := configuration.ConsumerConnectionConfig
	msgRepo := configuration.MsgRepo
	lockRepo := configuration.LockRepo
	dispatcherImpl := &MessageDispatcherImpl{djRepo: djRepo, consumerRepo: consumerRepo, msgRepo: msgRepo, dispatcherStop: make(chan bool),
		workerPool: make(chan chan *Job, brokerConfig.GetMaxWorkers()), jobPriorityQueue: NewJobPriorityQueue(), messageRecoverWorkerStop: make(chan bool),
		jobQueue: make(chan *Job, brokerConfig.GetMaxMessageQueueSize()), rationalDelay: brokerConfig.GetRationalDelay(), lockRepo: lockRepo,
		recoveryWorkersEnabled: brokerConfig.IsRecoveryWorkersEnabled(), jobRecoverStaleInflightWorkerStop: make(chan bool), jobRecoverRetryWorkerStop: make(chan bool),
		brokerConfig: brokerConfig}
	workers := make([]*Worker, brokerConfig.GetMaxWorkers())
	for i := 0; i < len(workers); i++ {
		worker := NewWorker(dispatcherImpl.workerPool, consumerConfig, brokerConfig, djRepo)
		worker.Start()
		workers[i] = &worker
	}
	dispatcherImpl.workers = workers
	dispatcherImpl.stopTimeout = consumerConfig.GetConnectionTimeout() + 250*time.Millisecond
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = int(brokerConfig.GetMaxWorkers())
	dispatcherImpl.StartDispatcher()
	return dispatcherImpl
}
