package data

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func getConsumer() *Consumer {
	consumer, _ := NewConsumer(getChannel(), "sample-consumer", "sample-token", sampleCallbackURL, "")
	consumer.QuickFix()
	return consumer
}

func getDeliveryJob() *DeliveryJob {
	job, _ := NewDeliveryJob(getCompleteMessageFixture(), getConsumer())
	return job
}

func TestDeliveryJobQuickFix(t *testing.T) {
	t.Run("NoQuickFix", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Status = JobDead
		testTime := time.Now().Add(-1 * time.Hour)
		job.DispatchReceivedAt = testTime
		job.StatusChangedAt = testTime
		job.EarliestNextAttemptAt = testTime
		assert.Equal(t, false, job.QuickFix())
		assert.Equal(t, testTime, job.DispatchReceivedAt)
		assert.Equal(t, testTime, job.StatusChangedAt)
		assert.Equal(t, testTime, job.EarliestNextAttemptAt)
		assert.Equal(t, JobDead, job.Status)
		assert.Equal(t, uint(0), job.RetryAttemptCount)
		job.Status = JobInflight
		assert.Equal(t, false, job.QuickFix())
		job.Status = JobDelivered
		assert.Equal(t, false, job.QuickFix())
	})
	t.Run("BaseFix", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.BasePaginateable.CreatedAt = time.Time{}
		assert.Equal(t, true, job.QuickFix())
	})
	t.Run("DispatchReceivedAtFix", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.DispatchReceivedAt = time.Time{}
		assert.Equal(t, true, job.QuickFix())
	})
	t.Run("StatusChangedAtFix", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.StatusChangedAt = time.Time{}
		assert.Equal(t, true, job.QuickFix())
	})
	t.Run("EarliestNextAttemptFix", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.EarliestNextAttemptAt = time.Time{}
		assert.Equal(t, true, job.QuickFix())
	})
	t.Run("StatusFix", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Status = JobStatus(0)
		assert.Equal(t, true, job.QuickFix())
	})
}

func TestDeliveryJobIsInValidState(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		assert.Equal(t, true, job.IsInValidState())
	})
	t.Run("MessageNil", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Message = nil
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("ListenerNil", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Listener = nil
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("MessageInvalid", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Message = &Message{}
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("ListenerInvalid", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Listener = &Consumer{}
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("InvalidStatus", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Status = JobStatus(12)
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("ValidStatus", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.Status = JobDead
		assert.Equal(t, true, job.IsInValidState())
		job.Status = JobDelivered
		assert.Equal(t, true, job.IsInValidState())
		job.Status = JobInflight
		assert.Equal(t, true, job.IsInValidState())
		job.Status = JobQueued
		assert.Equal(t, true, job.IsInValidState())
	})
	t.Run("ZeroReceivedAt", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.DispatchReceivedAt = time.Time{}
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("ZeroStatusChangedAt", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.StatusChangedAt = time.Time{}
		assert.Equal(t, false, job.IsInValidState())
	})
	t.Run("ZeroEarliestNextAt", func(t *testing.T) {
		t.Parallel()
		job := getDeliveryJob()
		job.EarliestNextAttemptAt = time.Time{}
		assert.Equal(t, false, job.IsInValidState())
	})
}

func TestNewDeliveryJob_InvalidParams(t *testing.T) {
	job, err := NewDeliveryJob(nil, nil)
	assert.Equal(t, ErrInsufficientInformationForCreating, err)
	assert.NotNil(t, job)
}

func TestDJGetNewLockID(t *testing.T) {
	job := getDeliveryJob()
	lock, err := NewLock(job)
	assert.Nil(t, err)
	assert.Equal(t, deliverJobLockPrefix+job.ID.String(), lock.LockID)
}

func TestDJString(t *testing.T) {
	assert.Equal(t, JobDeadStr, JobDead.String())
	assert.Equal(t, JobDeliveredStr, JobDelivered.String())
	assert.Equal(t, JobInflightStr, JobInflight.String())
	assert.Equal(t, JobQueuedStr, JobQueued.String())
	assert.Equal(t, "1", JobStatus(1).String())
}
