package scheduler

import (
	"sync"
	"time"

	"github.com/newscred/webhook-broker/config"
	"github.com/newscred/webhook-broker/dispatcher"
	"github.com/newscred/webhook-broker/storage"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/zerolog/log"
)

const (
	panicString = "parameters null"
)

// MessageScheduler is the contract for the scheduler service
type MessageScheduler interface {
	// Start begins the scheduler service
	Start()
	// Stop halts the scheduler service
	Stop()
}

// SchedulerConfiguration represents the configuration for the scheduler
type SchedulerConfiguration struct {
	ScheduledMsgRepo storage.ScheduledMessageRepository
	MsgRepo          storage.MessageRepository
	DispatcherSvc    dispatcher.MessageDispatcher
	SchedulerCfg     config.SchedulerConfig
	LockRepo         storage.LockRepository
}

func NewSchedulerConfiguration(scheduledMsgRepo storage.ScheduledMessageRepository, msgRepo storage.MessageRepository, dispatcherSvc dispatcher.MessageDispatcher, lockRepo storage.LockRepository, schedulerCfg config.SchedulerConfig) *SchedulerConfiguration {
	return &SchedulerConfiguration{
		ScheduledMsgRepo: scheduledMsgRepo,
		MsgRepo:          msgRepo,
		DispatcherSvc:    dispatcherSvc,
		LockRepo:         lockRepo,
		SchedulerCfg:     schedulerCfg,
	}
}

// MessageSchedulerImpl is the implementation of the scheduler service
type MessageSchedulerImpl struct {
	scheduledMsgRepo storage.ScheduledMessageRepository
	msgRepo          storage.MessageRepository
	dispatcherSvc    dispatcher.MessageDispatcher
	schedulerConfig  config.SchedulerConfig
	lockRepo         storage.LockRepository
	stopChan         chan struct{}
	wg               sync.WaitGroup
	metricsCollector *MetricsContainer
}

// NewMessageScheduler creates a new message scheduler service
func NewMessageScheduler(configuration *SchedulerConfiguration) MessageScheduler {
	if configuration.ScheduledMsgRepo == nil || configuration.MsgRepo == nil ||
		configuration.DispatcherSvc == nil || configuration.SchedulerCfg == nil ||
		configuration.LockRepo == nil {
		panic(panicString)
	}

	scheduler := &MessageSchedulerImpl{
		scheduledMsgRepo: configuration.ScheduledMsgRepo,
		msgRepo:          configuration.MsgRepo,
		dispatcherSvc:    configuration.DispatcherSvc,
		schedulerConfig:  configuration.SchedulerCfg,
		lockRepo:         configuration.LockRepo,
		stopChan:         make(chan struct{}),
		metricsCollector: NewMetricsContainer(),
	}

	return scheduler
}

// Start begins the scheduler processing loop
func (scheduler *MessageSchedulerImpl) Start() {
	scheduler.wg.Add(1)
	go func() {
		defer scheduler.wg.Done()
		ticker := time.NewTicker(scheduler.schedulerConfig.GetSchedulerInterval())
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				scheduler.processScheduledMessages()
			case <-scheduler.stopChan:
				return
			}
		}
	}()
}

// Stop halts the scheduler processing loop
func (scheduler *MessageSchedulerImpl) Stop() {
	close(scheduler.stopChan)
	scheduler.wg.Wait()
}

// processScheduledMessages retrieves messages due for dispatch and processes them
func (scheduler *MessageSchedulerImpl) processScheduledMessages() {
	messages := scheduler.scheduledMsgRepo.GetMessagesReadyForDispatch(scheduler.schedulerConfig.GetSchedulerBatchSize())

	for _, scheduledMsg := range messages {
		go scheduler.dispatchMessage(scheduledMsg)
	}
}

// dispatchMessage creates a regular message from a scheduled message and dispatches it
func (scheduler *MessageSchedulerImpl) dispatchMessage(scheduledMsg *data.ScheduledMessage) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("scheduledMsg", scheduledMsg).Msg("Recovered from panic in dispatchMessage")
			scheduler.metricsCollector.IncreaseSchedulingErrorCount()
		}
	}()

	err := inLockRun(scheduler.lockRepo, scheduledMsg, func() error {
		// Create regular message from scheduled message
		message, err := data.NewMessage(
			scheduledMsg.BroadcastedTo,
			scheduledMsg.ProducedBy,
			scheduledMsg.Payload,
			scheduledMsg.ContentType,
			scheduledMsg.Headers,
		)
		if err != nil {
			log.Error().Err(err).Str("messageId", scheduledMsg.MessageID).Msg("Failed to create message from scheduled message")
			scheduler.metricsCollector.IncreaseSchedulingErrorCount()
			return err
		}

		// Use the same Message ID for continuity
		message.MessageID = scheduledMsg.MessageID
		message.Priority = scheduledMsg.Priority

		// Create the message in the regular message table
		err = scheduler.msgRepo.Create(message)
		if err != nil {
			if err == storage.ErrDuplicateMessageIDForChannel {
				// Handle potential race condition
				time.Sleep(100 * time.Millisecond)
				refreshedMsg, getErr := scheduler.scheduledMsgRepo.GetByID(scheduledMsg.ID.String())
				if getErr == nil && refreshedMsg.Status == data.ScheduledMsgStatusScheduled {
					log.Error().Str("messageId", message.MessageID).Msg("Race condition detected: Message already created but status not updated")
				}
			} else {
				log.Error().Err(err).Str("messageId", message.MessageID).Msg("Failed to create message from scheduled message")
				scheduler.metricsCollector.IncreaseSchedulingErrorCount()
			}
			return err
		}

		// Update scheduled message status to dispatched and set dispatchedDate
		dispatchTime := time.Now()
		scheduledMsg.Status = data.ScheduledMsgStatusDispatched
		scheduledMsg.DispatchedDate = dispatchTime
		dispatchLag := dispatchTime.Sub(scheduledMsg.DispatchSchedule)
		scheduler.metricsCollector.SetLatestDispatchLag(dispatchLag)

		err = scheduler.scheduledMsgRepo.MarkDispatched(scheduledMsg)
		if err != nil {
			log.Error().Err(err).Str("messageId", message.MessageID).Msg("Failed to mark scheduled message as dispatched")
			scheduler.metricsCollector.IncreaseSchedulingErrorCount()
			return err
		}

		// Dispatch message through normal flow
		scheduler.dispatcherSvc.Dispatch(message)
		scheduler.metricsCollector.IncreaseDispatchedScheduledMessageCount()
		return nil
	})

	if err != nil {
		log.Error().Err(err).Str("messageId", scheduledMsg.MessageID).Msg("Error dispatching scheduled message")
	}
}

// inLockRun acquires a lock, runs a function, and releases the lock
func inLockRun(lockRepo storage.LockRepository, lockable data.Lockable, run func() error) (err error) {
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
