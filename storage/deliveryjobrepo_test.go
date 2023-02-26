package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/newscred/webhook-broker/storage/data"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
)

var (
	consumers []*data.Consumer
)

const (
	consumerIDPrefix = "test-consumer-for-dj-"
	messagePriority  = 5
)

func SetupForDeliveryJobTests() {
	testConsumers := 10
	consumerRepo := getConsumerRepo()
	consumers = make([]*data.Consumer, 0, testConsumers)
	for i := 0; i < testConsumers; i++ {
		consumer, _ := data.NewConsumer(channel1, consumerIDPrefix+strconv.Itoa(i), successfulGetTestToken, callbackURL, "")
		consumer.QuickFix()
		consumerRepo.Store(consumer)
		consumers = append(consumers, consumer)
	}
}

func getDeliverJobRepository() DeliveryJobRepository {
	return NewDeliveryJobRepository(testDB, getMessageRepository(), getConsumerRepo())
}

func getMessageForJob() *data.Message {
	message, _ := data.NewMessage(channel1, producer1, samplePayload, sampleContentType)
	return message
}

func getDeliveryJobsInFixture(message *data.Message) (jobs []*data.DeliveryJob) {
	jobs = make([]*data.DeliveryJob, 0, len(consumers))
	for _, consumer := range consumers {
		job, _ := data.NewDeliveryJob(message, consumer)
		jobs = append(jobs, job)
	}
	return jobs
}

func TestDispatchMessage(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		// FIXME: Split test into their own isolated test
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		message.Priority = messagePriority
		msgRepo.Create(message)
		jobs := getDeliveryJobsInFixture(message)
		err := djRepo.DispatchMessage(message, jobs...)
		assert.Nil(t, err)
		// Asserts for SetDispatched
		assert.Equal(t, data.MsgStatusDispatched, message.Status)
		assert.Greater(t, message.OutboxedAt.UnixNano(), message.ReceivedAt.UnixNano())
		assert.Greater(t, message.UpdatedAt.UnixNano(), message.CreatedAt.UnixNano())
		count := 0
		testDB.QueryRow("select count(*) from job where messageId like ?", message.ID).Scan(&count)
		assert.Equal(t, len(consumers), count)
		// Asserts for GetJobsForMessage
		dJobs, page, err := djRepo.GetJobsForMessage(message, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.Equal(t, len(consumers), len(dJobs))
		for _, dJob := range dJobs {
			assert.Equal(t, message, dJob.Message)
			assert.Contains(t, dJob.Listener.ConsumerID, consumerIDPrefix)
			assert.Equal(t, data.JobQueued, dJob.Status)
			assert.Equal(t, message.Priority, dJob.Priority)
		}
		_, _, err = djRepo.GetJobsForMessage(message, page)
		assert.Equal(t, ErrPaginationDeadlock, err)
		// Asserts for conjunction pagination query append
		originalPage := *page
		page.Previous = nil
		dJobs, _, err = djRepo.GetJobsForMessage(message, page)
		assert.Nil(t, err)
		assert.Equal(t, 0, len(dJobs))
		page = &originalPage
		page.Next = nil
		dJobs, _, err = djRepo.GetJobsForMessage(message, page)
		assert.Nil(t, err)
		assert.Equal(t, 0, len(dJobs))
	})
	t.Run("NoJobs", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		msgRepo.Create(message)
		err := djRepo.DispatchMessage(message)
		assert.Nil(t, err)
		// Asserts for SetDispatched
		assert.Equal(t, data.MsgStatusDispatched, message.Status)
		assert.Greater(t, message.OutboxedAt.UnixNano(), message.ReceivedAt.UnixNano())
		assert.Greater(t, message.UpdatedAt.UnixNano(), message.CreatedAt.UnixNano())
		count := 0
		testDB.QueryRow("select count(*) from job where messageId like ?", message.ID).Scan(&count)
		assert.Equal(t, 0, count)
	})
	t.Run("MessageAlreadyDispatched", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		msgRepo := getMessageRepository()
		message := getMessageForJob()
		msgRepo.Create(message)
		parallelMsg, _ := msgRepo.GetByID(message.ID.String())
		tx, _ := testDB.Begin()
		msgRepo.SetDispatched(context.WithValue(context.Background(), txContextKey, tx), parallelMsg)
		tx.Commit()
		jobs := getDeliveryJobsInFixture(message)
		err := djRepo.DispatchMessage(message, jobs...)
		assert.NotNil(t, err)
		assert.Equal(t, ErrNoRowsUpdated, err)
		count := -1
		testDB.QueryRow("select count(*) from job where messageId like ?", message.ID).Scan(&count)
		assert.Equal(t, 0, count)
	})
	t.Run("MsgNil", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(nil, jobs...))
	})
	t.Run("MsgInvalid", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		message.ReceivedAt = time.Time{}
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, jobs...))
	})
	t.Run("AJobNil", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, nil))
	})
	t.Run("AJobInvalid", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		jobs[0].DispatchReceivedAt = time.Time{}
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, jobs...))
	})
	t.Run("AJobWithDiffMsg", func(t *testing.T) {
		t.Parallel()
		djRepo := getDeliverJobRepository()
		message := getMessageForJob()
		jobs := getDeliveryJobsInFixture(message)
		newMsg := *message
		newMsg.ID = xid.New()
		jobs[0].Message = &newMsg
		assert.Equal(t, ErrInvalidStateToSave, djRepo.DispatchMessage(message, jobs...))
	})
}

func TestStatusUpdatesForJob(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err := djRepo.DispatchMessage(message, jobs...)
	assert.Nil(t, err)
	t.Run("GetByID", func(t *testing.T) {
		t.Parallel()
		iJob := jobs[0]
		dJob, err := djRepo.GetByID(iJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobQueued, dJob.Status)
		_, err = djRepo.GetByID("random-does-not-exist")
		assert.NotNil(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
	t.Run("MarkJobInflight", func(t *testing.T) {
		t.Parallel()
		job := jobs[1]
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobInflight(job)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobInflight, dJob.Status)
	})
	t.Run("MarkJobDead", func(t *testing.T) {
		t.Parallel()
		job := jobs[2]
		err = djRepo.MarkJobDead(job)
		assert.NotNil(t, err)
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(job)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobDead, dJob.Status)
	})
	t.Run("MarkJobDelivered", func(t *testing.T) {
		t.Parallel()
		job := jobs[3]
		err = djRepo.MarkJobDelivered(job)
		assert.NotNil(t, err)
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDelivered(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobDelivered(job)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobDelivered, dJob.Status)
	})
	t.Run("MarkJobRetry", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		next := 10 * time.Minute
		job := jobs[4]
		err = djRepo.MarkJobRetry(job, next)
		assert.NotNil(t, err)
		err := djRepo.MarkJobInflight(job)
		assert.Nil(t, err)
		err = djRepo.MarkJobRetry(job, next)
		assert.Nil(t, err)
		err = djRepo.MarkJobRetry(job, next)
		assert.NotNil(t, err)
		dJob, err := djRepo.GetByID(job.ID.String())
		assert.Equal(t, data.JobQueued, dJob.Status)
		assert.Greater(t, dJob.EarliestNextAttemptAt.UnixNano(), now.UnixNano())
	})
}

func TestStatusBasedJobsListing(t *testing.T) {
	t.Run("SuccessRetryList", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		oldLogger := log.Logger
		log.Logger = log.Output(&buf)
		defer func() { log.Logger = oldLogger }()
		errString := "sample select error"
		expectedErr := errors.New(errString)
		db, mock, _ := sqlmock.New()
		djRepo := &DeliveryJobDBRepository{db: db}
		mock.ExpectQuery(jobCommonSelectQuery).WillReturnError(expectedErr)
		mock.MatchExpectationsInOrder(true)
		jobs := djRepo.GetJobsInflightSince(configuration.RationalDelay)
		assert.Equal(t, 0, len(jobs))
		assert.Contains(t, buf.String(), errString)

	})
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()
	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err := djRepo.DispatchMessage(message, jobs...)
	djRepo.MarkJobInflight(jobs[0])
	assert.Nil(t, err)
	time.Sleep(configuration.RationalDelay + 1)
	t.Run("SuccessInflightRecoveryList", func(t *testing.T) {
		t.Parallel()
		thisJobs := djRepo.GetJobsInflightSince(configuration.RationalDelay)
		assert.LessOrEqual(t, 1, len(thisJobs))
		found := false
		for _, job := range thisJobs {
			if job.ID == jobs[0].ID {
				found = true
			}
		}
		assert.True(t, found)
	})
	t.Run("SuccessRetryList", func(t *testing.T) {
		t.Parallel()
		thisJobs := djRepo.GetJobsReadyForInflightSince(configuration.RationalDelay)
		assert.LessOrEqual(t, len(jobs)-1, len(thisJobs))
		found := false
		for index := 1; index < len(jobs); index++ {
			for _, job := range thisJobs {
				if job.ID == jobs[index].ID {
					found = true
				}
			}
			assert.True(t, found)
		}
	})
}

func TestGetJobsForConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	message2 := getMessageForJob()
	msgRepo.Create(message2)
	jobs2 := getDeliveryJobsInFixture(message2)
	err := djRepo.DispatchMessage(message2, jobs2...)
	assert.Nil(t, err)

	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err = djRepo.DispatchMessage(message, jobs...)
	testJob := jobs[5]
	djRepo.MarkJobInflight(testJob)
	assert.Nil(t, err)
	t.Run("PaginationDeadlock", func(t *testing.T) {
		t.Parallel()
		_, _, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, data.NewPagination(testJob, testJob))
		assert.Equal(t, ErrPaginationDeadlock, err)
	})
	t.Run("Success", func(t *testing.T) {
		t.Parallel()
		rJobs, page, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, data.NewPagination(nil, nil))
		assert.Nil(t, err)
		assert.LessOrEqual(t, 1, len(rJobs))
		assert.NotNil(t, page.Next)
		assert.NotNil(t, page.Previous)
		found := false
		for _, job := range rJobs {
			if job.ID == testJob.ID {
				found = true
			}
			assert.Equal(t, job.Listener.ID, testJob.Listener.ID)
			assert.Equal(t, data.JobInflight, job.Status)
		}
		assert.True(t, found)
		rJobs, page2, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, &data.Pagination{Previous: page.Previous})
		assert.Equal(t, 0, len(rJobs))
		assert.Nil(t, page2.Next)
		assert.Nil(t, page2.Previous)
		rJobs, page3, err := djRepo.GetJobsForConsumer(testJob.Listener, data.JobInflight, &data.Pagination{Next: page.Next})
		assert.Equal(t, 0, len(rJobs))
		assert.Nil(t, page3.Next)
		assert.Nil(t, page3.Previous)
	})
}

func TestRequeueDeadJobsForConsumer(t *testing.T) {
	djRepo := getDeliverJobRepository()
	msgRepo := getMessageRepository()

	message2 := getMessageForJob()
	msgRepo.Create(message2)
	jobs2 := getDeliveryJobsInFixture(message2)
	err := djRepo.DispatchMessage(message2, jobs2...)
	assert.Nil(t, err)

	message := getMessageForJob()
	msgRepo.Create(message)
	jobs := getDeliveryJobsInFixture(message)
	err = djRepo.DispatchMessage(message, jobs...)
	testJobs := []*data.DeliveryJob{jobs[5], jobs2[5]}
	for _, testJob := range testJobs {
		err := djRepo.MarkJobInflight(testJob)
		assert.Nil(t, err)
		err = djRepo.MarkJobDead(testJob)
		assert.Nil(t, err)
	}
	rJobs, _, err := djRepo.GetJobsForConsumer(testJobs[0].Listener, data.JobDead, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	assert.LessOrEqual(t, 2, len(rJobs))
	for _, job := range rJobs {
		found := false
		for _, testJob := range testJobs {
			if job.ID == testJob.ID {
				found = true
			}

			assert.Equal(t, job.Listener.ID, testJob.Listener.ID)
			assert.Equal(t, data.JobDead, job.Status)
		}
		assert.True(t, found)
	}
	err = djRepo.RequeueDeadJobsForConsumer(testJobs[0].Listener)
	assert.Nil(t, err)
	rJobs, _, err = djRepo.GetJobsForConsumer(testJobs[0].Listener, data.JobDead, data.NewPagination(nil, nil))
	assert.Nil(t, err)
	assert.LessOrEqual(t, 0, len(rJobs))
	for _, testJob := range testJobs {
		job, err := djRepo.GetByID(testJob.ID.String())
		assert.Nil(t, err)
		assert.Equal(t, data.JobQueued, job.Status)
	}
}
