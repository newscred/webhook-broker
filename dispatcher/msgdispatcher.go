package dispatcher

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
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
	scheduler                         *laneScheduler
	schedulerStop                     chan struct{}
	schedulerStopOnce                 sync.Once
	assignerDone                      chan struct{}
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
		log.Error().Err(err).Msg("error dispatching")
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
		if job.Listener.Type == data.PushConsumer {
			msgDispatcher.jobQueue <- NewJob(job)
		}
	}

	genericPanicRecoveryFunc = func() {
		if r := recover(); r != nil {
			errorLogger := log.Error()
			extension := "none"
			if rStr, ok := r.(string); ok {
				extension = rStr
			}
			errorLogger.Msg("Had to recover from panic: " + extension)
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
				log.Error().Err(err).Msg("error - could ensure dispatch from recover worker " + message.MessageID)
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
		jobs := msgDispatcher.djRepo.GetJobsReadyForInflightSince(msgDispatcher.rationalDelay, int(msgDispatcher.brokerConfig.GetMaxRetry()))
		for _, job := range jobs {
			if job.Listener.Type == data.PullConsumer {
				if job.RetryAttemptCount >= uint(msgDispatcher.brokerConfig.GetMaxRetry()) {
					err := inLockRun(msgDispatcher.lockRepo, job, func() error {
						return msgDispatcher.djRepo.MarkQueuedJobAsDead(job)
					})
					if err != nil {
						log.Error().Err(err).Msg("error - could not mark job dead " + job.ID.String())
					}
				}
			} else {
				if msgDispatcher.scheduler.isHeld(job.ID.String()) {
					// Already queued or running here; the scheduler would drop it anyway. Skipping it before taking the
					// lock saves a lock insert and delete per held job on every pass, which matters when a consumer at
					// its max concurrency keeps a large backlog.
					continue
				}
				err := inLockRun(msgDispatcher.lockRepo, job, func() error {
					queueJob(msgDispatcher, job)
					return nil
				})
				if err != nil {
					log.Error().Err(err).Msg("error - could not retry job" + job.ID.String())
				}
			}
		}
	}

	shouldEnqueueLongInflightJob = func(job *data.DeliveryJob, msgDispatcher *MessageDispatcherImpl) bool {
		totalDuration := time.Duration(job.IncrementalTimeout)*time.Second + msgDispatcher.rationalDelay + msgDispatcher.stopTimeout
		changeAllowedThreshold := time.Now().Add(-totalDuration)
		return job.Listener.Type == data.PushConsumer || job.Listener.Type == data.PullConsumer && job.StatusChangedAt.Before(changeAllowedThreshold)
	}

	recoverJobsFromLongInflight = func(msgDispatcher *MessageDispatcherImpl) {
		defer genericPanicRecoveryFunc()
		jobs := msgDispatcher.djRepo.GetJobsInflightSince(msgDispatcher.stopTimeout + msgDispatcher.rationalDelay)
		for _, job := range jobs {
			if !shouldEnqueueLongInflightJob(job, msgDispatcher) {
				continue
			}
			// Ignore max retry intentionally since we are recovering likely from a process crash during delivery.
			err := inLockRun(msgDispatcher.lockRepo, job, func() error {
				msgDispatcher.djRepo.MarkJobRetry(job, computeEarliestDelta(job.RetryAttemptCount+1, msgDispatcher.brokerConfig))
				return nil
			})
			if err != nil {
				log.Error().Err(err).Msg("error - could not requeue job" + job.ID.String())
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
	go msgDispatcher.assignJobsToWorkers()
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
		log.Error().Msg("warn - dispatcher stop timedout")
		return
	default:
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			msgDispatcher.dispatcherStop <- true
			msgDispatcher.schedulerStopOnce.Do(func() { close(msgDispatcher.schedulerStop) })
			if msgDispatcher.recoveryWorkersEnabled {
				msgDispatcher.messageRecoverWorkerStop <- true
				msgDispatcher.jobRecoverRetryWorkerStop <- true
				msgDispatcher.jobRecoverStaleInflightWorkerStop <- true
			}
			// Nothing enqueues any more, so the jobs still waiting can be dropped.
			msgDispatcher.scheduler.discardQueued()
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
		msgDispatcher.releaseUnreadJobs(timeoutContext)
	}
}

// releaseUnreadJobs frees the slot of each job that the assign loop gave to a worker which then
// stopped without reading it. The job stays queued in the database for the next dispatcher.
func (msgDispatcher *MessageDispatcherImpl) releaseUnreadJobs(ctx context.Context) {
	select {
	case <-msgDispatcher.assignerDone:
	default:
		select {
		case <-msgDispatcher.assignerDone:
		case <-ctx.Done():
			return
		}
	}
	for _, worker := range msgDispatcher.workers {
		select {
		case job := <-worker.jobChannel:
			msgDispatcher.scheduler.done(job)
		default:
		}
	}
}

// assignJobsToWorkers waits for an idle worker, then asks the scheduler for the next job. The job
// is chosen only once a worker is free, so the round-robin across consumers sees the latest state.
func (msgDispatcher *MessageDispatcherImpl) assignJobsToWorkers() {
	defer close(msgDispatcher.assignerDone)
	for {
		var jobChannel chan *Job
		select {
		case jobChannel = <-msgDispatcher.workerPool:
		case <-msgDispatcher.schedulerStop:
			return
		}
		job := msgDispatcher.scheduler.next(msgDispatcher.schedulerStop)
		if job == nil {
			return
		}
		jobChannel <- job
	}
}

func (msgDispatcher *MessageDispatcherImpl) dispatchJob(job *Job) {
	msgDispatcher.scheduler.enqueue(job)
}

// Configuration represents the configuration for a dispatcher
type Configuration struct {
	DeliveryJobRepo          storage.DeliveryJobRepository
	ConsumerRepo             storage.ConsumerRepository
	LockRepo                 storage.LockRepository
	MsgRepo                  storage.MessageRepository
	BrokerConfig             config.BrokerConfig
	ConsumerConnectionConfig config.ConsumerConnectionConfig
	MetricsCollector         *MetricsContainer
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
		workerPool: make(chan chan *Job, brokerConfig.GetMaxWorkers()), scheduler: newLaneScheduler(brokerConfig.GetConsumerMaxConcurrency, configuration.MetricsCollector), schedulerStop: make(chan struct{}), assignerDone: make(chan struct{}),
		messageRecoverWorkerStop: make(chan bool), jobQueue: make(chan *Job, brokerConfig.GetMaxMessageQueueSize()),
		rationalDelay: brokerConfig.GetRationalDelay(), lockRepo: lockRepo, recoveryWorkersEnabled: brokerConfig.IsRecoveryWorkersEnabled(),
		jobRecoverStaleInflightWorkerStop: make(chan bool), jobRecoverRetryWorkerStop: make(chan bool), brokerConfig: brokerConfig}
	workers := make([]*Worker, brokerConfig.GetMaxWorkers())
	for i := 0; i < len(workers); i++ {
		worker := NewWorker(dispatcherImpl.workerPool, consumerConfig, brokerConfig, djRepo)
		worker.onDone = dispatcherImpl.scheduler.done
		worker.Start()
		workers[i] = &worker
	}
	dispatcherImpl.workers = workers
	dispatcherImpl.stopTimeout = consumerConfig.GetConnectionTimeout() + 250*time.Millisecond
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = int(brokerConfig.GetMaxWorkers())
	dispatcherImpl.StartDispatcher()
	return dispatcherImpl
}
