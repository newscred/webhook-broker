package data

import (
	"strconv"
	"time"
)

// JobStatus represents the delivery job status
type JobStatus int

func (status JobStatus) String() string {
	switch status {
	case JobQueued:
		return JobQueuedStr
	case JobInflight:
		return JobInflightStr
	case JobDelivered:
		return JobDeliveredStr
	case JobDead:
		return JobDeadStr
	default:
		return strconv.Itoa(int(status))
	}
}

const (
	deliverJobLockPrefix = "dj-"
	// JobQueued is the job status during first attempt
	JobQueued JobStatus = iota + 1000
	// JobInflight is to signify that the DeliveryJob is in its first attempt
	JobInflight
	// JobDelivered signifies that the DeliveryJob received 2XX status from consumer
	JobDelivered
	// JobDead signifies that retry has taken its toll and max retried happened
	JobDead
	// JobQueuedStr is the string rep of JobQueued
	JobQueuedStr = "QUEUED"
	// JobInflightStr is the string rep of JobInflight
	JobInflightStr = "INFLIGHT"
	// JobDeliveredStr is the string rep of JobDelivered
	JobDeliveredStr = "DELIVERED"
	// JobDeadStr is the string rep of JobDead
	JobDeadStr = "DEAD"
)

// DeliveryJob represents the DTO object for deliverying a Message to a consumer
type DeliveryJob struct {
	BasePaginateable
	Message               *Message
	Listener              *Consumer
	Status                JobStatus
	StatusChangedAt       time.Time
	DispatchReceivedAt    time.Time
	EarliestNextAttemptAt time.Time
	RetryAttemptCount     uint
	Priority              uint
	IncrementalTimeout    uint // in seconds
}

// QuickFix fixes the object state automatically as much as possible
func (job *DeliveryJob) QuickFix() bool {
	madeChanges := job.BasePaginateable.QuickFix()
	if job.DispatchReceivedAt.IsZero() {
		job.DispatchReceivedAt = time.Now()
		job.EarliestNextAttemptAt = time.Now()
		madeChanges = true
	}
	if job.StatusChangedAt.IsZero() {
		job.StatusChangedAt = time.Now()
		madeChanges = true
	}
	if job.EarliestNextAttemptAt.IsZero() {
		job.EarliestNextAttemptAt = job.DispatchReceivedAt
		madeChanges = true
	}
	switch job.Status {
	case JobQueued:
	case JobInflight:
	case JobDelivered:
	case JobDead:
	default:
		job.Status = JobQueued
		madeChanges = true
	}
	return madeChanges
}

// IsInValidState returns false if any of message id or payload or content type is empty, channel is nil, callback URL is not url or not absolute URL,
// status not recognized, received at and outboxed at not set properly. Call QuickFix before IsInValidState is called.
func (job *DeliveryJob) IsInValidState() bool {
	valid := true
	if job.Message == nil || !job.Message.IsInValidState() || job.Listener == nil || !job.Listener.IsInValidState() {
		valid = false
	}
	if valid && job.Status != JobQueued && job.Status != JobInflight && job.Status != JobDelivered && job.Status != JobDead {
		valid = false
	}
	if valid {
		if job.DispatchReceivedAt.IsZero() {
			valid = false
		} else if job.StatusChangedAt.IsZero() {
			valid = false
		} else if job.EarliestNextAttemptAt.IsZero() {
			valid = false
		}
	}
	return valid
}

// GetLockID retrieves the Lock ID representing this instance of DeliveryJob
func (job *DeliveryJob) GetLockID() string {
	return deliverJobLockPrefix + job.ID.String()
}

// NewDeliveryJob creates a new instance of DeliveryJob; returns insufficient info error if parameters are not valid for a new DeliveryJob
func NewDeliveryJob(msg *Message, consumer *Consumer) (job *DeliveryJob, err error) {
	job = &DeliveryJob{Message: msg, Listener: consumer}
	if msg != nil {
		job.Priority = msg.Priority
	}

	job.QuickFix()
	if !job.IsInValidState() {
		err = ErrInsufficientInformationForCreating
	}
	return job, err
}
